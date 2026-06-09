package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/installer"
)

// setupCmd performs first-time setup: layout dirs, auth token, farmhand.yaml,
// daemon registration, optional Cloudflare config. Idempotent — re-running
// detects existing artifacts and asks before overwriting.
var setupCmd = &cobra.Command{
	Use:          "setup",
	Short:        "Configure FarmHand on this host: layout, config, daemon",
	Long:         "Generate /opt/farmhand layout, auth token, farmhand.yaml, and register the FarmHand daemon (systemd on Linux, launchd LaunchAgent on macOS). Run interactively by default; pass --yes for unattended/CI use.",
	SilenceUsage: true,
	RunE:         runSetup,
}

func init() {
	setupCmd.Flags().String("install-dir", "/opt/farmhand", "FarmHand install directory (config, db, runtime dirs)")
	setupCmd.Flags().Int("port", 8080, "HTTP port to listen on")
	setupCmd.Flags().String("host", "", "bind host; defaults to 127.0.0.1 when --tunnel-host is set, else 0.0.0.0")
	setupCmd.Flags().String("token", "", "auth token (generated if empty)")
	setupCmd.Flags().Bool("no-token", false, "leave auth_token empty (requires dev_mode)")
	setupCmd.Flags().Bool("dev-mode", false, "enable dev_mode in the generated config (auth disabled)")
	setupCmd.Flags().Bool("no-daemon", false, "skip systemd/launchd registration")
	setupCmd.Flags().String("tunnel-host", "", "Cloudflare tunnel hostname (e.g. devices-2.example.com); enables tunnel config")
	setupCmd.Flags().String("tunnel-id", "", "Cloudflare tunnel UUID (from `cloudflared tunnel create`)")
	setupCmd.Flags().String("tunnel-name", "", "Cloudflare tunnel name (used in `route dns` instructions)")
	setupCmd.Flags().String("adb-path", "", "path to adb binary (auto-detected if empty)")
	setupCmd.Flags().String("webhook-url", "", "webhook URL for job notifications (empty disables)")
	setupCmd.Flags().Int("max-concurrent-jobs", 3, "maximum jobs that may execute in parallel")
	setupCmd.Flags().String("vision-api-key-env", "MINIMAX_API_KEY", "env var holding the vision LLM API key")
	setupCmd.Flags().String("ios-simulators", "", "comma-separated iOS simulator UDIDs or names (macOS only)")
	setupCmd.Flags().Bool("yes", false, "assume defaults / yes to every prompt")
}

func runSetup(cmd *cobra.Command, _ []string) error {
	plat, err := installer.DetectPlatform()
	if err != nil {
		return err
	}

	installDir, _ := cmd.Flags().GetString("install-dir")
	port, _ := cmd.Flags().GetInt("port")
	host, _ := cmd.Flags().GetString("host")
	token, _ := cmd.Flags().GetString("token")
	noToken, _ := cmd.Flags().GetBool("no-token")
	devMode, _ := cmd.Flags().GetBool("dev-mode")
	noDaemon, _ := cmd.Flags().GetBool("no-daemon")
	tunnelHost, _ := cmd.Flags().GetString("tunnel-host")
	tunnelID, _ := cmd.Flags().GetString("tunnel-id")
	tunnelName, _ := cmd.Flags().GetString("tunnel-name")
	adbPath, _ := cmd.Flags().GetString("adb-path")
	webhookURL, _ := cmd.Flags().GetString("webhook-url")
	maxJobs, _ := cmd.Flags().GetInt("max-concurrent-jobs")
	visionKeyEnv, _ := cmd.Flags().GetString("vision-api-key-env")
	iosSimsCSV, _ := cmd.Flags().GetString("ios-simulators")
	assumeYes, _ := cmd.Flags().GetBool("yes")

	p := installer.NewPrompter(os.Stdin, os.Stderr, assumeYes)

	fmt.Fprintf(os.Stderr, "farmhand setup — %s/%s\n", plat.OS, plat.Arch)

	// Interactive overrides for unset flags.
	if !assumeYes {
		if installDir, err = p.Ask("Install directory", installDir); err != nil {
			return err
		}
		portStr, err := p.Ask("HTTP port", strconv.Itoa(port))
		if err != nil {
			return err
		}
		if n, err := strconv.Atoi(portStr); err == nil {
			port = n
		}
		if host == "" {
			defaultHost := "0.0.0.0"
			if tunnelHost != "" {
				defaultHost = "127.0.0.1"
			}
			if host, err = p.Ask("Bind host", defaultHost); err != nil {
				return err
			}
		}

		// ADB path: auto-detect, then let user confirm/override.
		if adbPath == "" {
			if detected, err := exec.LookPath("adb"); err == nil {
				adbPath = detected
			} else {
				adbPath = "adb"
			}
		}
		if adbPath, err = p.Ask("Path to adb (Android device support)", adbPath); err != nil {
			return err
		}

		// iOS simulators — macOS-only prompt.
		if plat.OS == "darwin" && iosSimsCSV == "" {
			hint := "comma-separated UDIDs or names; empty disables (list with: xcrun simctl list devices)"
			if iosSimsCSV, err = p.Ask("iOS simulators to manage ("+hint+")", ""); err != nil {
				return err
			}
		}

		// Concurrency cap.
		jobsStr, err := p.Ask("Max concurrent jobs", strconv.Itoa(maxJobs))
		if err != nil {
			return err
		}
		if n, err := strconv.Atoi(jobsStr); err == nil && n > 0 {
			maxJobs = n
		}

		// Notifications webhook (optional).
		if webhookURL, err = p.Ask("Webhook URL for job notifications (empty = none)", webhookURL); err != nil {
			return err
		}

		// Vision API key env var name.
		if visionKeyEnv, err = p.Ask("Env var holding the vision LLM API key", visionKeyEnv); err != nil {
			return err
		}
	} else {
		if host == "" {
			if tunnelHost != "" {
				host = "127.0.0.1"
			} else {
				host = "0.0.0.0"
			}
		}
		if adbPath == "" {
			if detected, err := exec.LookPath("adb"); err == nil {
				adbPath = detected
			} else {
				adbPath = "adb"
			}
		}
	}

	// Token: generate unless user supplied one, or --no-token / --dev-mode.
	if !noToken && !devMode && token == "" {
		token, err = installer.GenerateAuthToken()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Generated auth_token (43 chars). Save it: %s\n", token)
	}
	if noToken && !devMode {
		return fmt.Errorf("--no-token requires --dev-mode (otherwise the API would refuse every request)")
	}

	layout := installer.DerivedLayout(installDir)

	// Layout dirs require root on /opt; warn early if not root.
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "(warning) not running as root — re-running under sudo for /opt mkdir, daemon install, etc.")
	}

	if err := writeRuntimeDirs(layout); err != nil {
		return err
	}

	cfg := installer.DefaultConfig(layout, port, host, token, devMode)
	// Apply user-customisable fields on top of defaults.
	cfg.Devices.ADBPath = adbPath
	cfg.Jobs.MaxConcurrentJobs = maxJobs
	cfg.Notifications.WebhookURL = webhookURL
	if visionKeyEnv != "" {
		cfg.Vision.APIKeyEnv = visionKeyEnv
	}
	if iosSimsCSV != "" {
		cfg.Devices.IOSSimulators = splitAndTrim(iosSimsCSV)
	}

	writeConfigFile := true
	if _, statErr := os.Stat(layout.ConfigPath); statErr == nil && !assumeYes {
		confirm, err := p.Confirm(fmt.Sprintf("%s already exists. Overwrite?", layout.ConfigPath), false)
		if err != nil {
			return err
		}
		writeConfigFile = confirm
	}
	if writeConfigFile {
		if err := installer.WriteConfig(layout.ConfigPath, cfg, true); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", layout.ConfigPath)
	} else {
		fmt.Fprintf(os.Stderr, "keeping existing %s — daemon will be refreshed to point at it\n", layout.ConfigPath)
	}

	// Daemon
	if !noDaemon {
		dm := plat.DetectDaemonManager()
		switch dm {
		case installer.DaemonSystemd:
			if err := installer.InstallSystemd(layout); err != nil {
				return err
			}
			if err := installer.StartSystemd(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "installed and started systemd unit farmhand.service")
		case installer.DaemonLaunchd:
			if err := installer.InstallLaunchd(layout); err != nil {
				return err
			}
			if err := installer.StartLaunchd(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "installed and loaded LaunchAgent io.kanolab.farmhand")
		case installer.DaemonNone:
			fmt.Fprintln(os.Stderr, "no service manager detected — skipping daemon install")
		}
	}

	// Cloudflare tunnel (optional, partial automation)
	if tunnelHost != "" {
		if tunnelID == "" {
			return fmt.Errorf("--tunnel-host requires --tunnel-id (run `cloudflared tunnel create <name>` first)")
		}
		t := installer.CloudflareTunnel{
			TunnelID:   tunnelID,
			TunnelName: tunnelName,
			Hostname:   tunnelHost,
			ServerPort: port,
		}
		followUps, err := installer.InstallCloudflareConfig(t)
		if err != nil {
			return fmt.Errorf("cloudflared config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "wrote /etc/cloudflared/config.yml. Finish with:\n")
		for _, c := range followUps {
			fmt.Fprintf(os.Stderr, "  %s\n", c)
		}
	}

	fmt.Fprintf(os.Stderr, "\nDone. curl http://localhost:%d/api/v1/health to confirm.\n", port)
	return nil
}

// writeRuntimeDirs makes sure all layout dirs exist. On Linux systemd
// installs the daemon-user chown happens later in InstallSystemd; here we
// just mkdir.
func writeRuntimeDirs(layout installer.Layout) error {
	for _, dir := range layout.RuntimeDirs() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// splitAndTrim parses a comma-separated user input into a clean slice.
// Empty tokens are dropped so trailing commas don't produce blank entries.
func splitAndTrim(csv string) []string {
	out := []string{}
	for _, p := range strings.Split(csv, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
