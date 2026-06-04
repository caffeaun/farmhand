package main

import (
	"github.com/spf13/cobra"
)

// launchCmd starts the main launcher activity of an installed Android
// package via `am start --pn <pkg>` under the hood. The package id is
// validated at the bridge against the Android package-id regex before
// reaching the device shell.
var launchCmd = &cobra.Command{
	Use:          "launch",
	Short:        "Launch an Android app by package id",
	Long:         "Start the main launcher activity of --package on the device with --device. The package id must look like `com.example.app` (lowercase, dot-separated). Equivalent to `adb shell am start --pn <pkg>`; the bridge validates the id before shelling out so it cannot inject extra adb arguments.",
	SilenceUsage: true,
	RunE:         runLaunch,
}

func init() {
	launchCmd.Flags().String("device", "", "device ID (required)")
	launchCmd.Flags().String("package", "", "Android package id, e.g. com.example.app (required)")
	_ = launchCmd.MarkFlagRequired("device")
	_ = launchCmd.MarkFlagRequired("package")
}

func runLaunch(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")
	pkg, _ := cmd.Flags().GetString("package")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	return mgr.Launch(id, pkg)
}
