package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
)

// The test binary is also the af a local box runs: linked as af it is this binary's af, and
// brain.DefaultAFPath is that link, so that a fix measured in a local box never runs the
// tests again.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		main()
	}
	dir, af, err := fakeaf.LinkSelf()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot link the test binary as af: %v\n", err)
		os.Exit(1)
	}
	brain.DefaultAFPath = af
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
