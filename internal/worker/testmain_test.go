package worker

import (
	"context"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
)

// The test binary is also the af a box runs: linked as af it is the real af. The af of a box has
// the identity of the af of the worker under test, v-test and c-test, or the worker refuses the
// image.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(afcmd.New(&afcmd.Config{Version: "v-test", Commit: "c-test"}).Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
