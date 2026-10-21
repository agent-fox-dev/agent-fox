package brain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// The real af, for the tests that run the hook and af <agent> submit as a fake claude
// does: it is built once for the tests of the package, from the module, and removed
// when they are over.
var (
	builtOnce sync.Once
	builtDir  string
	builtAF   string
	builtErr  error
)

func realAF(t *testing.T) string {
	t.Helper()
	builtOnce.Do(func() {
		if builtDir, builtErr = os.MkdirTemp("", "af-real-*"); builtErr != nil {
			return
		}
		builtAF = filepath.Join(builtDir, "af")
		cmd := exec.Command("go", "build", "-o", builtAF, "github.com/agent-fox-dev/agentfox/cmd/af")
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			builtErr = &buildError{err: err, out: string(out)}
		}
	})
	if builtErr != nil {
		t.Skipf("af cannot be built here: %v", builtErr)
	}
	return builtAF
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

// removeBuiltAF removes the af that realAF built.
func removeBuiltAF() {
	if builtDir != "" {
		_ = os.RemoveAll(builtDir)
	}
}

// claudeWorld is a world with a box of the provider that the claude entry asks for, the
// fake claude, and the claude brain made as the command makes it.
type claudeWorld struct {
	*procWorld
	brain Brain
}

// newClaudeWorld opens the box with the request that BoxRequest makes for claude on the
// provider, from the host environment (set in the process, so that a leak would show),
// and makes the brain. With real, af is the real one.
func newClaudeWorld(t *testing.T, provider string, host map[string]string, model string, real bool) *claudeWorld {
	t.Helper()
	for name, value := range host {
		t.Setenv(name, value)
	}
	bin, af, claude := fakeBin(t)
	if real {
		af = realAF(t)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := newRepo(t)
	rd := newRunDir(t, dir)
	entry, err := Default.Lookup(ClaudeName)
	if err != nil {
		t.Fatal(err)
	}
	providerEntry, err := box.Default.Lookup(provider)
	if err != nil {
		t.Fatal(err)
	}
	req := BoxRequest(BoxParams{
		Brain: entry, Provider: providerEntry, Getenv: os.LookupEnv, Tools: []string{"read_file", "list_files"},
		HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {},
	})
	s, err := box.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	pw := &procWorld{world: &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}, bin: bin, af: af, claude: claude}
	opts := Options{Model: model, Provider: provider, Getenv: os.LookupEnv, AFPath: af, AFVersion: "v1.0.0", AFCommit: "abc123"}
	b, err := Default.New(ClaudeName, opts)
	if err != nil {
		t.Fatal(err)
	}
	pw.p = NewProcess(opts.ProcessConfig())
	return &claudeWorld{procWorld: pw, brain: b}
}

// answers reads the answers a fake claude noted in the file.
func (w *claudeWorld) answers(path string) []fakeAnswer {
	w.t.Helper()
	data, err := w.boxFile(path)
	if err != nil {
		w.t.Fatalf("%s: %v", path, err)
	}
	var out []fakeAnswer
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		w.t.Fatal(err)
	}
	return out
}
