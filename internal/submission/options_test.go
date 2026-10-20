package submission

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestOnRefusedHearsEveryRefusalAndOnlyRefusals guards the extension that a
// kind with a record of its own needs: the hook is called, with the target and
// every problem, for each way a payload is refused, and for no submission that
// is accepted; what it writes through the target lands beside the submission,
// and cannot change the refusal.
func TestOnRefusedHearsEveryRefusalAndOnlyRefusals(t *testing.T) {
	var calls [][]Problem
	var targets []Target
	kind := problemsKind(Problem{Path: "/a", Message: "first"}, Problem{Path: "/b", Message: "second"})
	kind.OnRefused = func(ctx context.Context, target Target, problems []Problem) {
		calls = append(calls, problems)
		targets = append(targets, target)
		_ = target.Write(ctx, ".agent-fox/run/"+target.RunID+"/noted.txt", []byte("refused"))
		panic("a hook that fails is not the refusal's business") // recovered by Submit
	}

	t.Run("the kind refuses", func(t *testing.T) {
		calls, targets = nil, nil
		tr := newTree(t, "test")

		_, err := Submit(context.Background(), tr.target, kind, []byte(`{"a":1}`), fixedNow)

		r := refused(t, err)
		if len(r.Problems) != 2 || len(calls) != 1 || len(calls[0]) != 2 || calls[0][0].Message != "first" {
			t.Fatalf("refusal %v, hook calls %v, want one call with both problems", r.Problems, calls)
		}
		if targets[0].RunID != runID || targets[0].Tree == nil {
			t.Errorf("target = %+v", targets[0])
		}
		if data, err := os.ReadFile(filepath.Join(tr.dir, ".agent-fox", "run", runID, "noted.txt")); err != nil || string(data) != "refused" {
			t.Errorf("what the hook wrote beside the submission: %q, %v", data, err)
		}
		if _, ok := tr.submission(); ok {
			t.Error("a submission was written")
		}
	})

	t.Run("the payload has the wrong shape", func(t *testing.T) {
		calls = nil
		tr := newTree(t, "test")
		if _, err := Submit(context.Background(), tr.target, kind, []byte(`[1,2]`), fixedNow); refused(t, err) == nil || len(calls) != 1 || len(calls[0]) != 1 {
			t.Errorf("hook calls = %v, want one call with the one problem of the shape", calls)
		}
	})
	t.Run("run.json is missing", func(t *testing.T) {
		calls = nil
		tr := newTree(t, "")
		if _, err := Submit(context.Background(), tr.target, kind, []byte(`{"a":1}`), fixedNow); refused(t, err) == nil || len(calls) != 1 {
			t.Errorf("hook calls = %v, want one", calls)
		}
	})
	t.Run("a submission that is accepted", func(t *testing.T) {
		calls = nil
		k := echoKind()
		k.OnRefused = kind.OnRefused
		tr := newTree(t, "test")
		if _, err := Submit(context.Background(), tr.target, k, []byte(`{"a":1}`), fixedNow); err != nil || len(calls) != 0 {
			t.Errorf("err = %v, hook calls = %v, want none", err, calls)
		}
	})
	t.Run("a run id that names a path is not a refusal of the payload", func(t *testing.T) {
		calls = nil
		tr := newTree(t, "test")
		target := tr.target
		target.RunID = ".."
		if _, err := Submit(context.Background(), target, kind, []byte(`{"a":1}`), fixedNow); err == nil || len(calls) != 0 {
			t.Errorf("err = %v, hook calls = %v, want an error and no call", err, calls)
		}
	})
}

// TestCommandWithAddsWhatItIsGiven guards CommandWith: the kind is made from the
// directory of the run, Prepare runs before the payload is read, the result and
// its schema are the options', and a refusal gets none of that result.
func TestCommandWithAddsWhatItIsGiven(t *testing.T) {
	var dirs []string
	var order []string
	type extra struct {
		Path string `json:"path"`
		Seen bool   `json:"seen"`
	}
	opts := Options{
		Kind: func(dir string) Kind {
			dirs = append(dirs, dir)
			return citeKind()
		},
		ResultSchema: json.RawMessage(`{"type":"object","properties":{"seen":{"type":"boolean"}}}`),
		Prepare: func(r *toolio.Run, run *toolio.RunDir) error {
			order = append(order, "prepare:"+run.ID)
			return nil
		},
		Result: func(r *toolio.Run, run *toolio.RunDir, receipt Receipt) (any, error) {
			order = append(order, "result:"+receipt.Path)
			return extra{Path: receipt.Path, Seen: true}, nil
		},
	}
	build := func() *toolio.App {
		a := &toolio.App{Version: "test"}
		a.Register(CommandWith("fake submit", echoKind(), opts))
		return a
	}

	t.Run("an accepted submission", func(t *testing.T) {
		dirs, order = nil, nil
		tr := newTree(t, "test")

		r := run(t, build(), `{"summary":"done"}`, submitArgs(tr)...)

		if r.code != 0 || r.field("result", "seen") != true || r.field("result", "path") != submissionFile {
			t.Fatalf("exit code %d, result %v\n%s", r.code, r.field("result"), r.stdout)
		}
		want := []string{"prepare:" + runID, "result:" + submissionFile}
		if !slices.Equal(order, want) {
			t.Errorf("order = %v, want %v", order, want)
		}
		if len(dirs) != 1 || !filepath.IsAbs(dirs[0]) {
			t.Errorf("the kind was made from %v, want the absolute --dir once", dirs)
		}
	})
	t.Run("the kind made from the directory is the one that validates", func(t *testing.T) {
		tr := newTree(t, "test")
		r := run(t, build(), `{"cite":"README.md"}`, submitArgs(tr)...)
		if r.code != 1 || r.field("error", "category") != "submission" {
			t.Errorf("exit code %d, category %v, want citeKind's refusal\n%s", r.code, r.field("error", "category"), r.stdout)
		}
	})
	t.Run("a refusal gets no result", func(t *testing.T) {
		order = nil
		tr := newTree(t, "test")
		r := run(t, build(), `[1]`, submitArgs(tr)...)
		if r.code != 1 || r.field("result") != nil || slices.ContainsFunc(order, func(s string) bool { return strings.HasPrefix(s, "result:") }) {
			t.Errorf("exit code %d, result %v, order %v", r.code, r.field("result"), order)
		}
	})
	t.Run("a Prepare that fails ends the command", func(t *testing.T) {
		failing := opts
		failing.Prepare = func(*toolio.Run, *toolio.RunDir) error {
			return &toolio.Failure{Category: toolio.CategoryIO, Message: "cannot prepare"}
		}
		a := &toolio.App{Version: "test"}
		a.Register(CommandWith("fake submit", echoKind(), failing))
		tr := newTree(t, "test")
		r := run(t, a, `{"a":1}`, submitArgs(tr)...)
		if r.code != 1 || r.field("error", "category") != "io" {
			t.Errorf("exit code %d, category %v", r.code, r.field("error", "category"))
		}
		if _, ok := tr.submission(); ok {
			t.Error("a submission was written after Prepare failed")
		}
	})
	t.Run("a Result that fails is the command's failure", func(t *testing.T) {
		failing := opts
		failing.Result = func(*toolio.Run, *toolio.RunDir, Receipt) (any, error) { return nil, errors.New("cannot report") }
		a := &toolio.App{Version: "test"}
		a.Register(CommandWith("fake submit", echoKind(), failing))
		r := run(t, a, `{"a":1}`, submitArgs(newTree(t, "test"))...)
		if r.code == 0 {
			t.Errorf("exit code 0, want the failure of Result")
		}
	})
}
