package agentrun

import (
	"context"
	"strings"
	"testing"
)

// Issue #196: a writing phase that names the gate's test suite may not run it
// — the program runs it after the phase and judges by that run — so the
// suite, a make target that wraps it, and `go test` over a whole module are
// refused with the targeted form; the linter and targeted runs are not.
func TestGuardRefusesTheWholeSuiteInAPhaseThatNamesIt(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"go", "make", "gofmt"}, AllowOperators: true,
		Suite: []string{"make test"}})
	ctx := context.Background()
	for _, cmd := range []string{
		"make test",
		"make test lint",
		"make check",
		"go test ./... -count=1",
		"go test -count=1 ./...",
		"make lint && make test",
		"GOFLAGS=-mod=mod make test",
	} {
		d := g(ctx, execCall(cmd))
		if !d.Block {
			t.Errorf("allowed: %s", cmd)
			continue
		}
		if !strings.Contains(d.Reason, "go test ./pkg -run Name") || !strings.Contains(d.Reason, "after you submit") {
			t.Errorf("%s: the reason does not say what to run instead: %s", cmd, d.Reason)
		}
	}
	if d := g(ctx, argvCall("make", "test")); !d.Block {
		t.Error("allowed: run_command make test")
	}
	for _, cmd := range []string{
		"make lint",
		"go test ./internal/x -run TestY",
		"go test ./internal/x/...",
		"go vet ./...",
		"gofmt -l .",
	} {
		if d := g(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %s: %s", cmd, d.Reason)
		}
	}

	// A phase that names no suite — the repair, or any other — runs it.
	plain := Guard(GuardOptions{Programs: []string{"go", "make"}, AllowOperators: true})
	for _, cmd := range []string{"make test", "go test ./... -count=1", "make check"} {
		if d := plain(ctx, execCall(cmd)); d.Block {
			t.Errorf("a phase with no suite refused %s: %s", cmd, d.Reason)
		}
	}
}
