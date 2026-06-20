package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/platform"
	"github.com/zzet/gortex/internal/telemetry"
)

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Show or change anonymous usage telemetry (on / off / status)",
	Long: `Anonymous usage telemetry records coarse, bucketed counts of which tools and
commands run — never code, file paths, symbol names, or exact counts. It is
OPT-IN: off by default, and nothing is recorded or sent until you enable it.

  gortex telemetry status   # what's enabled, why, and what's collected
  gortex telemetry on       # enable
  gortex telemetry off      # disable and delete any buffered data

Precedence (highest first): GORTEX_TELEMETRY env, DO_NOT_TRACK env, your saved
choice, then the off-by-default. DO_NOT_TRACK and GORTEX_TELEMETRY=0 always win.`,
}

var telemetryOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Enable anonymous usage telemetry",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := telemetry.SaveConsent(platform.TelemetryDir(), true, "cli", time.Now); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Telemetry enabled — anonymous tool/command counts only (no code, paths, or names).")
		return nil
	},
}

var telemetryOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Disable anonymous usage telemetry and delete any buffered data",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := telemetry.SaveConsent(platform.TelemetryDir(), false, "cli", time.Now); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Telemetry disabled — buffered data cleared.")
		return nil
	},
}

var telemetryStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether telemetry is enabled, why, and what is collected",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir := platform.TelemetryDir()
		consent := telemetry.ResolveConsent(telemetry.LoadConsentConfig(dir), nil)
		out := cmd.OutOrStdout()
		state := "disabled"
		if consent.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(out, "Telemetry: %s (decided by: %s)\n", state, consent.Source)
		endpoint := "not configured — nothing is transmitted"
		if v := os.Getenv(telemetry.EnvEndpoint); v != "" {
			endpoint = v
		}
		fmt.Fprintf(out, "Ingest endpoint: %s\n", endpoint)
		fmt.Fprintf(out, "Install id: %s\n", telemetry.InstallID(dir))
		fmt.Fprintln(out, "Collected: coarse counts of which tools/commands run, bucketed (no code, paths, names, or exact counts).")
		fmt.Fprintln(out, "Change with: gortex telemetry on | off")
		return nil
	},
}

func init() {
	telemetryCmd.AddCommand(telemetryOnCmd, telemetryOffCmd, telemetryStatusCmd)
	rootCmd.AddCommand(telemetryCmd)
}
