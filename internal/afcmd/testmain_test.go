package afcmd

import (
	"context"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
)

// The test binary is also the af a box runs: linked as af it is the real af (the app of
// this package).
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(newApp().Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
