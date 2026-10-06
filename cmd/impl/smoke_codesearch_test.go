package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/envtest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// implBranch is the branch impl names for the example spec.
const implBranch = "impl/09-agent-mode-spec-cli"

// implSmokeTurns scripts the model for the example spec: a survey, then three
// tasks. Each phase opens with a code_search call, so the call's place in the
// probe's sequence says where the phase began.
func implSmokeTurns() []faux.Turn {
	search := func(id string) faux.Turn {
		return indextest.ToolTurn(id, "code_search", map[string]any{"query": "spec"})
	}
	task := func(n, subject string, tests []string, doneWhen bool) []faux.Turn {
		var verdicts []map[string]any
		for _, id := range tests {
			verdicts = append(verdicts, map[string]any{
				"id": id, "verdict": "pass",
				"evidence":     "task" + n + ".go: " + id + " passes when run with make test",
				"red_evidence": "go test failed before the change: " + id + " got the zero value",
			})
		}
		args := map[string]any{
			"summary":        "implemented task " + n,
			"commit_subject": subject,
			"test_verdicts":  verdicts,
			"changes":        []map[string]any{{"path": "task" + n + ".go", "change": "added"}},
		}
		if doneWhen {
			args["done_when_verdicts"] = []map[string]any{{
				"id": "DW-1", "verdict": "pass", "evidence": "ran the command in done_when and it exited zero",
			}}
		}
		return []faux.Turn{
			search("s" + n),
			indextest.ToolTurn("w"+n, "write_file", map[string]any{"path": "task" + n + ".go", "content": "package x\n"}),
			indextest.ToolTurn("t"+n, "submit_task", args),
		}
	}
	turns := []faux.Turn{
		search("sv"),
		indextest.ToolTurn("sv2", "submit_survey", map[string]any{"summary": "nothing exists yet"}),
	}
	turns = append(turns, task("1", "feat: land task one", []string{"TS-09-1", "TS-09-2", "TS-09-3"}, false)...)
	turns = append(turns, task("2", "feat: land task two", []string{"TS-09-4", "TS-09-5"}, false)...)
	turns = append(turns, task("3", "feat: land task three", []string{"TS-09-6"}, true)...)
	return turns
}

// implSmokeRepo is the example spec's repository without the touches the
// scripted work would not honour, committed.
func implSmokeRepo(t *testing.T) string {
	t.Helper()
	dir, specDir := preflightSpecRepo(t)
	path := filepath.Join(specDir, "tasks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, task := range doc["tasks"].([]any) {
		delete(task.(map[string]any), "touches")
	}
	if raw, err = json.MarshalIndent(doc, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: spec without touches")
	return dir
}

// runImplSmoke drives impl's real App on dir with a recording index and a
// scripted model.
func runImplSmoke(t *testing.T, dir string) (int, map[string]any, string) {
	t.Helper()
	envtest.Clean(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(ctx, []string{"--dir", dir, "--land", "none", "--no-review", "09"},
		strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env, stdout.String() + "\n" + stderr.String()
}

// TS-16-34 (smoke): impl invalidates the index after the branch is in place
// and after each task's commit, before the next phase, and closes it on exit.
//
// Verifies: 16-PATH-3, 16-REQ-4.2, 16-REQ-4.5
//
// Real components: the cmd/impl App, toolio.App.Main, codeimpl.Run, agentrun.Runner, gitx.Git
func TestTS16_34_ImplInvalidatesTheIndexAcrossTreeChanges_Smoke(t *testing.T) {
	for _, tc := range []struct {
		name      string
		preBranch bool
	}{
		{"a new branch", false},
		{"an existing branch", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := implSmokeRepo(t)
			if tc.preBranch {
				gitIn(t, dir, "branch", implBranch)
			}
			probe := &indextest.Probe{}
			t.Cleanup(toolio.SetIndexBuilder(func(ws *tools.Workspace) (tools.Index, error) {
				probe.Root = ws.Root
				return probe, nil
			}))

			p := faux.New(implSmokeTurns()...)
			t.Cleanup(toolio.SetRunnerConfigHook(func(cfg *agentrun.Config) {
				cfg.Model = faux.Model()
				cfg.Thinking = core.ThinkingUnset
				cfg.Providers = core.ProviderRegistry{faux.API: p.APIProvider()}
			}))

			code, env, log := runImplSmoke(t, dir)
			res, _ := env["result"].(map[string]any)
			if code != toolio.ExitOK || res["stage"] != "landed" || res["tasks_done"] != float64(3) {
				t.Fatalf("exit %d, stage %v, tasks_done %v\n%s", code, res["stage"], res["tasks_done"], log)
			}

			// Every phase of the run was offered code_search.
			for i, names := range indextest.Offered(p) {
				if !indextest.Has(names, "code_search") {
					t.Errorf("request %d was not offered code_search: %v", i, names)
				}
			}

			// searches: the survey, then each task's phase.
			s := probe.Searches()
			if len(s) != 4 {
				t.Fatalf("code_search reached the index %d times, want 4 (survey and three tasks)", len(s))
			}

			onBranch := func(snaps []indextest.Snap) bool {
				for _, sn := range snaps {
					if sn.Rel == "" && sn.Branch == implBranch {
						return true
					}
				}
				return false
			}
			if tc.preBranch {
				// The existing branch is checked out in pre-flight, before the survey.
				if !onBranch(probe.Between(0, s[0])) {
					t.Errorf("no Invalidate(\"\") on %s before the survey: %+v", implBranch, probe.Snaps())
				}
			} else if !onBranch(probe.Between(s[0], s[1])) {
				// The survey runs before the branch is created, so the new branch
				// is invalidated before the first task's phase.
				t.Errorf("no Invalidate(\"\") on %s between the survey and task 1: %+v", implBranch, probe.Snaps())
			}

			// After each landed task's commit, before the next phase: HEAD is
			// that task's commit and the tree is clean.
			for i, subject := range []string{"task one", "task two"} {
				commit := gitIn(t, dir, "log", "--format=%H", "--grep", subject)
				if commit == "" || strings.Contains(commit, "\n") {
					t.Fatalf("commits for %q = %q, want exactly one", subject, commit)
				}
				ok := false
				for _, sn := range probe.Between(s[i+1], s[i+2]) {
					if sn.Rel == "" && sn.Head == commit && sn.Status == "" {
						ok = true
					}
				}
				if !ok {
					t.Errorf("no Invalidate(\"\") with HEAD at %s's commit %s before the next task's phase: %+v",
						subject, commit, probe.Between(s[i+1], s[i+2]))
				}
			}

			if n := probe.CloseCalls(); n != 1 {
				t.Errorf("the index was closed %d times, want exactly 1", n)
			}
		})
	}
}
