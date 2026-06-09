package installer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/caffeaun/farmhand/internal/installer/templates"
)

const (
	// launchdLabel is the LaunchAgent label, used as plist filename and
	// `launchctl list` key. Matches docs/use-cases/03-macmini-with-ios.md.
	launchdLabel = "io.kanolab.farmhand"
)

// launchdPlistPath returns the per-user LaunchAgent plist path. Per-user
// scope keeps device entitlements (xcrun, ADB) working without requiring
// LaunchDaemon (root) — see EXPLORE.md guidance.
func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

// launchdRenderContext is the template payload for farmhand.plist.tmpl.
type launchdRenderContext struct {
	Label      string
	BinaryPath string
	ConfigPath string
	InstallDir string
	LogDir     string
}

// RenderLaunchdPlist returns the contents of the LaunchAgent plist.
func RenderLaunchdPlist(layout Layout) ([]byte, error) {
	tmpl, err := template.ParseFS(templates.FS, "farmhand.plist.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse launchd template: %w", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, launchdRenderContext{
		Label:      launchdLabel,
		BinaryPath: layout.BinaryPath,
		ConfigPath: layout.ConfigPath,
		InstallDir: layout.InstallDir,
		LogDir:     layout.LogDir,
	})
	if err != nil {
		return nil, fmt.Errorf("render launchd plist: %w", err)
	}
	return buf.Bytes(), nil
}

// InstallLaunchd writes the LaunchAgent plist. Caller invokes StartLaunchd
// separately to load it. Runs as the current user (no sudo needed for
// per-user LaunchAgent).
func InstallLaunchd(layout Layout) error {
	body, err := RenderLaunchdPlist(layout)
	if err != nil {
		return err
	}
	path := launchdPlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// StartLaunchd loads the LaunchAgent.
func StartLaunchd() error {
	out, err := exec.Command("launchctl", "load", "-w", launchdPlistPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestartLaunchd unloads then loads the LaunchAgent. Used after binary swap.
func RestartLaunchd() error {
	// `launchctl unload` is best-effort — if the agent isn't loaded it errors,
	// which is fine for the restart use case.
	_ = exec.Command("launchctl", "unload", launchdPlistPath()).Run()
	return StartLaunchd()
}

// StopLaunchd unloads the LaunchAgent. Best-effort.
func StopLaunchd() error {
	out, err := exec.Command("launchctl", "unload", launchdPlistPath()).CombinedOutput()
	// If it wasn't loaded, that's success in our intent.
	if err != nil && !strings.Contains(string(out), "Could not find specified service") {
		return fmt.Errorf("launchctl unload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveLaunchd unloads and deletes the plist.
func RemoveLaunchd() error {
	_ = StopLaunchd()
	if err := os.Remove(launchdPlistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", launchdPlistPath(), err)
	}
	return nil
}

// LaunchdStatus reports whether the LaunchAgent is loaded with a running PID.
// `launchctl list <label>` exits 0 if loaded, regardless of running state;
// the PID key in its dict output is the truth.
func LaunchdStatus() (active bool, raw string, err error) {
	out, runErr := exec.Command("launchctl", "list", launchdLabel).CombinedOutput()
	raw = strings.TrimSpace(string(out))
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return false, raw, fmt.Errorf("launchctl list: %w", runErr)
		}
		return false, raw, nil
	}
	// Look for a "PID" entry mapped to a non-zero number.
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"PID\" =") {
			continue
		}
		// Format: "PID" = 12345;
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "\"PID\" ="), ";"))
		if val != "0" && val != "" {
			return true, raw, nil
		}
	}
	return false, raw, nil
}
