package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/installer"
)

// updateCmd swaps the binary at /usr/local/bin/farmhand to the latest (or
// pinned) GitHub release for the current platform and restarts the daemon.
var updateCmd = &cobra.Command{
	Use:          "update",
	Short:        "Download the latest FarmHand release and restart the daemon",
	Long:         "Query github.com/caffeaun/farmhand for the latest release (or --version to pin), download the binary for this OS/arch, swap it atomically into /usr/local/bin/farmhand, then restart systemd/launchd. Requires root on Linux; on macOS only the mv step needs sudo.",
	SilenceUsage: true,
	RunE:         runUpdate,
}

func init() {
	updateCmd.Flags().String("version", "", "release tag to install (e.g. v0.6.3); empty = latest")
	updateCmd.Flags().Bool("no-restart", false, "skip daemon restart after binary swap")
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	plat, err := installer.DetectPlatform()
	if err != nil {
		return err
	}

	pinned, _ := cmd.Flags().GetString("version")
	noRestart, _ := cmd.Flags().GetBool("no-restart")

	var rel *installer.Release
	if pinned == "" {
		rel, err = installer.LatestRelease()
	} else {
		rel, err = installer.ReleaseByTag(pinned)
	}
	if err != nil {
		return err
	}
	url, err := rel.AssetURL(plat.AssetName())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "downloading %s %s\n", rel.TagName, url)

	tmp, err := installer.DownloadBinary(url, nil)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	layout := installer.DefaultLayout()
	if err := installer.AtomicSwapBinary(tmp, layout.BinaryPath); err != nil {
		return fmt.Errorf("swap binary: %w", err)
	}
	fmt.Fprintf(os.Stderr, "installed %s -> %s\n", rel.TagName, layout.BinaryPath)

	if noRestart {
		fmt.Fprintln(os.Stderr, "--no-restart given; remember to restart the daemon manually.")
		return nil
	}
	switch plat.DetectDaemonManager() {
	case installer.DaemonSystemd:
		if err := installer.RestartSystemd(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "restarted systemd farmhand.service")
	case installer.DaemonLaunchd:
		if err := installer.RestartLaunchd(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "reloaded LaunchAgent io.kanolab.farmhand")
	case installer.DaemonNone:
		fmt.Fprintln(os.Stderr, "no service manager detected — start the daemon manually.")
	}
	return nil
}
