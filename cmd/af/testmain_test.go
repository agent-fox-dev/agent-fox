package main

import (
	"context"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fakeclaude"
)

// The test binary is also the programs a box runs: linked as af it is the real af (the
// app of this package), and linked as claude it is the fake Claude Code of
// internal/fakeclaude, which runs the commands its script gives it, af among them.
func TestMain(m *testing.M) {
	switch fakeclaude.Name() {
	case fakeclaude.AF:
		os.Exit(newApp().Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	case fakeclaude.Claude:
		os.Exit(fakeclaude.Run(fakeclaude.Claude, os.Args[1:], fakeclaude.Options{}))
	}
	os.Exit(m.Run())
}
