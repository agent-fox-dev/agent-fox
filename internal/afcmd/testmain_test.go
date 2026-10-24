package afcmd

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
)

// The test binary is also the af a box runs: linked as af it is the real af (the app of
// this package). A local box runs the af of brain.DefaultAFPath, which is that link.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(newApp().Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
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
