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
		Suite: []string{"make test"}, TargetedRun: "go test ./pkg -run Name"})
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

// Issue #219: outside Go, the suite's own words are also the targeted form's
// words. A run that adds an operand — a test name, a file, `-- <file>` — or a
// flag that selects tests targets them and is allowed; one that adds nothing,
// or only flags, is the suite and is refused.
func TestTheSuiteRefusalAllowsTargetedRunsInEveryEcosystem(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		suite   string
		refused []string
		allowed []string
	}{
		{"cargo test", []string{"cargo test", "cargo test --release", "cargo test -- --nocapture"},
			[]string{"cargo test my_unit_test", "cargo test --release parse::tests"}},
		{"npm test", []string{"npm test", "npm test --"},
			[]string{"npm test -- src/x.test.ts"}},
		{"pytest -q", []string{"pytest -q", "pytest -q -x"},
			[]string{"pytest -q tests/test_x.py::test_y", "pytest -q -k parse"}},
		{"pytest", []string{"pytest", "pytest -q"},
			[]string{"pytest tests/test_x.py"}},
		{"uv run pytest -q", []string{"uv run pytest -q"},
			[]string{"uv run pytest -q tests/test_x.py"}},
		{"bundle exec rspec", []string{"bundle exec rspec"},
			[]string{"bundle exec rspec spec/x_spec.rb"}},
		{"mvn -q test", []string{"mvn -q test"},
			[]string{"mvn -q test -Dtest=ParserTest"}},
		{"go test ./... -count=1", []string{"go test ./... -count=1", "go test ./... -run X"},
			[]string{"go test ./pkg -run Name"}},
		{"make test", []string{"make test", "make test lint", "make check"},
			[]string{"make lint"}},
	} {
		prog := strings.Fields(tc.suite)[0]
		g := Guard(GuardOptions{Programs: []string{prog, "go", "make"}, AllowOperators: true,
			Suite: []string{tc.suite}})
		for _, cmd := range tc.refused {
			if d := g(ctx, execCall(cmd)); !d.Block {
				t.Errorf("suite %q: allowed %q", tc.suite, cmd)
			}
		}
		for _, cmd := range tc.allowed {
			if d := g(ctx, execCall(cmd)); d.Block {
				t.Errorf("suite %q: refused %q: %s", tc.suite, cmd, d.Reason)
			}
		}
	}
}

// Issue #219: the refusal names the project's own targeted form, not Go's.
func TestTheSuiteRefusalNamesTheProjectsTargetedForm(t *testing.T) {
	ctx := context.Background()
	g := Guard(GuardOptions{Programs: []string{"cargo"}, AllowOperators: true, Suite: []string{"cargo test"},
		TargetedRun: "cargo test <name>"})
	d := g(ctx, execCall("cargo test"))
	if !d.Block || !strings.Contains(d.Reason, "`cargo test <name>`") || strings.Contains(d.Reason, "go test ./pkg") {
		t.Errorf("reason = %q", d.Reason)
	}
	// With no targeted form known, the refusal names none rather than Go's.
	plain := Guard(GuardOptions{Programs: []string{"cargo"}, AllowOperators: true, Suite: []string{"cargo test"}})
	if d := plain(ctx, execCall("cargo test")); !d.Block || strings.Contains(d.Reason, "go test ./pkg") {
		t.Errorf("reason = %q", d.Reason)
	}
}
