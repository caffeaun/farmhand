// Package main is the entry point for the farmhand CLI.
package main

import (
	"fmt"
	"os"

	"github.com/caffeaun/farmhand/internal/config"
	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// cfg holds the loaded application configuration, accessible to all subcommands.
var cfg *config.Config

var rootCmd = &cobra.Command{
	Use:          "farmhand",
	Short:        "FarmHand — self-hosted mobile device farm",
	Long:         "FarmHand is an open-source, self-hosted mobile device farm platform for running end-to-end tests on real physical devices.",
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// --config is a persistent flag on the root command; cobra makes it
		// available via the root's PersistentFlags regardless of which
		// subcommand is executing.
		configPath, err := cmd.Root().PersistentFlags().GetString("config")
		if err != nil {
			return fmt.Errorf("reading --config flag: %w", err)
		}

		loaded, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		cfg = loaded
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().String("config", "farmhand.yaml", "path to config file")

	// --version prints "<name> <version>" and exits.
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("farmhand {{.Version}}\n")

	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(devicesCmd)
	rootCmd.AddCommand(runCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
