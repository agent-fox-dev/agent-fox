package worker

import (
	"context"
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/fakeclaude"
)

// The test binary is also the programs a box runs: linked as af it is the real af, and linked as
// claude it is the fake Claude Code of internal/fakeclaude. The af of a box has the identity of
// the af of the worker under test, v-test and c-test, or the worker refuses the image.
func TestMain(m *testing.M) {
	switch fakeclaude.Name() {
	case fakeclaude.AF:
		os.Exit(afcmd.New(&afcmd.Config{Version: "v-test", Commit: "c-test"}).Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	case fakeclaude.Claude:
		os.Exit(fakeclaude.Run(fakeclaude.Claude, os.Args[1:], fakeclaude.Options{}))
	}
	os.Exit(m.Run())
}
