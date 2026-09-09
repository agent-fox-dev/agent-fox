package main

import (
	"fmt"
	"io"
	"os"
)

// isAgentMode returns true when AF_AGENT=1 is set in the environment.
func isAgentMode() bool {
	return os.Getenv("AF_AGENT") == "1"
}

// shouldShowBanner determines whether the ASCII art banner should be
// displayed. The banner is suppressed when:
//   - --quiet is set
//   - AF_AGENT=1 is active
//   - the subcommand is validate, status, or list
//   - --json appears in the arguments
func shouldShowBanner(quiet bool, subcmd string, args []string) bool {
	if quiet || isAgentMode() {
		return false
	}
	switch subcmd {
	case "validate", "status", "list", "activate", "seal", "archive", "supersede":
		return false
	}
	for _, a := range args {
		if a == "--json" {
			return false
		}
	}
	return true
}

// printBanner writes the ASCII art banner to the given writer.
// The art matches packages/spec/spec/ui.py SPEC_ART exactly.
// The version is not embedded in the art; it is printed separately by the caller.
func printBanner(w io.Writer) {
	fmt.Fprintf(w, `
   ___ _ __   ___  ___
  / __| '_ \ / _ \/ __|
  \__ \ |_) |  __/ (__
  |___/ .__/ \___|\___|
      |_|
`)
}
