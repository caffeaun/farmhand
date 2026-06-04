package main

import (
	"github.com/spf13/cobra"
)

// clearCmd kills every background app on the target device (via the
// ADBBridge's KillAllApps) and then sends KEYCODE_HOME to return to the
// launcher — the canonical "clean state" before running a new test.
//
// Both manager calls share the standard 5-guard surface; the HOME key
// is dispatched even if KillAllApps reported no processes to kill, so
// failures from the second call still surface to the CLI exit code.
var clearCmd = &cobra.Command{
	Use:          "clear",
	Short:        "Kill background apps on an Android device and return to the launcher",
	Long:         "Run `am kill-all` on the device with --device, then send KEYCODE_HOME so the launcher is foregrounded. Useful as a `setUp`/`tearDown` step between tests to guarantee a known starting state.",
	SilenceUsage: true,
	RunE:         runClear,
}

func init() {
	clearCmd.Flags().String("device", "", "device ID (required)")
	_ = clearCmd.MarkFlagRequired("device")
}

func runClear(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	if err := mgr.KillAllApps(id); err != nil {
		return err
	}
	return mgr.KeyEvent(id, "KEYCODE_HOME")
}
