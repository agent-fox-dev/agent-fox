package submission

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
