package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/config"
	"github.com/caffeaun/farmhand/internal/installer"
)

var doctorCmd = &cobra.Command{
	Use:          "doctor",
	Short:        "Diagnose a FarmHand install: daemon, /health, adb, paths",
	Long:         "Run a series of checks against an existing FarmHand install. Each row reports OK or a one-line failure detail. Non-zero exit if any check fails.",
	SilenceUsage: true,
	RunE:         runDoctor,
}

func init() {
	doctorCmd.Flags().String("install-dir", "/opt/farmhand", "install directory to diagnose")
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	installDir, _ := cmd.Flags().GetString("install-dir")
	layout := installer.DerivedLayout(installDir)

	// `cfg` (the package-level global loaded by PersistentPreRunE) is read
	// from --config, default "farmhand.yaml" relative to cwd. From an
	// unrelated working directory, that's the defaults — which makes the
	// health-endpoint check probe the wrong port. Prefer the install dir's
	// canonical config; fall back to the prerun cfg if explicit --config
	// was given but reading the install-dir copy failed.
	doctorCfg := cfg
	if loaded, err := config.Load(layout.ConfigPath); err == nil {
		doctorCfg = loaded
	}

	results := installer.RunDoctor(layout, doctorCfg)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CHECK\tSTATUS\tDETAIL")
	failures := 0
	for _, r := range results {
		status := "OK"
		if !r.OK {
			status = "FAIL"
			failures++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Name, status, r.Detail)
	}
	w.Flush()
	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}
