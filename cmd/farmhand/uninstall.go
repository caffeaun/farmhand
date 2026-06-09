package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/installer"
)

var uninstallCmd = &cobra.Command{
	Use:          "uninstall",
	Short:        "Stop the FarmHand daemon and remove the service file",
	Long:         "Stop the systemd unit or launchd LaunchAgent and remove the service file. Does NOT remove /opt/farmhand or the database unless --purge is given.",
	SilenceUsage: true,
	RunE:         runUninstall,
}

func init() {
	uninstallCmd.Flags().Bool("purge", false, "also remove /opt/farmhand (database, artifacts, logs)")
	uninstallCmd.Flags().Bool("yes", false, "assume yes to confirmation prompts")
}

func runUninstall(cmd *cobra.Command, _ []string) error {
	plat, err := installer.DetectPlatform()
	if err != nil {
		return err
	}
	purge, _ := cmd.Flags().GetBool("purge")
	assumeYes, _ := cmd.Flags().GetBool("yes")

	p := installer.NewPrompter(os.Stdin, os.Stderr, assumeYes)
	confirm, err := p.Confirm("Stop the farmhand daemon and remove its service file?", true)
	if err != nil {
		return err
	}
	if !confirm {
		return fmt.Errorf("aborted by user")
	}

	switch plat.DetectDaemonManager() {
	case installer.DaemonSystemd:
		if err := installer.StopSystemd(); err != nil {
			return err
		}
		if err := installer.RemoveSystemd(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "removed systemd unit farmhand.service")
	case installer.DaemonLaunchd:
		if err := installer.RemoveLaunchd(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "removed LaunchAgent io.kanolab.farmhand")
	}

	if purge {
		layout := installer.DefaultLayout()
		confirm, err := p.Confirm(fmt.Sprintf("--purge: also delete %s and everything in it?", layout.InstallDir), false)
		if err != nil {
			return err
		}
		if confirm {
			if err := os.RemoveAll(layout.InstallDir); err != nil {
				return fmt.Errorf("remove %s: %w", layout.InstallDir, err)
			}
			fmt.Fprintf(os.Stderr, "removed %s\n", layout.InstallDir)
		}
	}
	fmt.Fprintln(os.Stderr, "uninstall complete. The /usr/local/bin/farmhand binary is left in place — `sudo rm /usr/local/bin/farmhand` to remove it.")
	return nil
}
