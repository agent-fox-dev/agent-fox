package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is the version string reported by --version. main() copies the
// build-time value from the agentfox package into it.
var version = "dev"

// specDirDefault returns the default value for --spec-dir, honoring
// the SPEC_DIR env var (falls back to ".specs" if unset or empty).
func specDirDefault() string {
	if v := os.Getenv("SPEC_DIR"); v != "" {
		return v
	}
	return ".specs"
}

// dirValue is a pflag.Value that validates that the value is an
// existing directory when set.
type dirValue struct {
	val string
}

func (d *dirValue) String() string { return d.val }
func (d *dirValue) Type() string   { return "string" }

func (d *dirValue) Set(s string) error {
	info, err := os.Stat(s)
	if err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q: not a directory", s)
	}
	d.val = s
	return nil
}

// newRootCmd creates the root cobra command with all global flags.
func newRootCmd() *cobra.Command {
	var quiet bool
	var showVersion bool
	sourceVal := &dirValue{val: "."}

	cmd := &cobra.Command{
		Use:   "spec",
		Short: "Spec CLI — manage agentspec specifications",
		// Suppress the auto-injected cobra completion subcommand so that
		// the Go CLI surface area matches the Python CLI (which uses Click
		// and has no such command).
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// When invoked without subcommand, print help and exit 0.
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), version)
				return nil
			}
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Agent mode forces quiet.
			if isAgentMode() {
				quiet = true
			}

			// Determine the subcommand name for banner suppression.
			subcmdName := ""
			if cmd.Name() != "spec" {
				subcmdName = cmd.Name()
			}

			// Show banner if appropriate.
			if shouldShowBanner(quiet, subcmdName, os.Args) {
				cwd, _ := os.Getwd()
				printBanner(cmd.ErrOrStderr())
				fmt.Fprintf(cmd.ErrOrStderr(), "spec v%s  %s\n\n", version, cwd)
			}

			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Register global persistent flags.
	// Default for spec-dir comes from SPEC_DIR env var or ".specs".
	cmd.PersistentFlags().StringP("spec-dir", "d", specDirDefault(), "spec directory path")
	cmd.PersistentFlags().VarP(sourceVal, "source", "s", "source directory (must exist)")
	cmd.PersistentFlags().Lookup("source").DefValue = "."
	cmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress progress output")

	// Register --version flag on root command only.
	cmd.Flags().BoolVar(&showVersion, "version", false, "print version and exit")

	// Register subcommands.
	cmd.AddCommand(newNewCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newRefineCmd())
	cmd.AddCommand(newGenerateCmd())
	cmd.AddCommand(newRenderCmd())
	cmd.AddCommand(newValidateCmd())
	cmd.AddCommand(newLintCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newCampaignCmd())
	cmd.AddCommand(newActivateCmd())
	cmd.AddCommand(newSealCmd())
	cmd.AddCommand(newArchiveCmd())
	cmd.AddCommand(newSupersedeCmd())

	return cmd
}

// Execute runs the root command. This is the binary entry point.
// It creates a root context with signal handling (SIGINT, SIGTERM)
// and passes it to all subcommands.
func Execute() {
	ctx, cancel := signalCtx(context.Background())
	defer cancel()

	cmd := newRootCmd()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err != nil {
		if isAgentMode() {
			// In agent mode, wrap errors as JSON on stdout.
			_ = emitError(err.Error())
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
