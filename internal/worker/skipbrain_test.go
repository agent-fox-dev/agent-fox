package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// submittedRun is a clone with a run directory that holds a submission the host accepted: the file, and the
// ledger that says so.
type submittedRun struct {
	t     *testing.T
	clone string
	run   *toolio.RunDir
	kind  jobs.Kind
	file  submission.File
}

// newSubmittedRun makes the clone, a git repository with the files, and the run of the kind with the
// payload, accepted and (for a fix report) applied.
func newSubmittedRun(t *testing.T, kind jobs.Kind, files map[string]string, kindName, payload string, applied bool) *submittedRun {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=x", "-c", "user.email=x@example.com", "commit", "-q", "-m", "initial"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &submittedRun{t: t, clone: dir, run: run, kind: kind}
	var compact json.RawMessage
	if got, problems := submission.Check(context.Background(), kindOf(kind, dir), os.DirFS(dir), []byte(payload)); len(problems) == 0 {
		compact = got
	} else {
		t.Fatalf("the payload of the test does not pass its own kind: %v", problems)
	}
	s.file = submission.File{Kind: kindName, RunID: run.ID, Attempt: 1, Nonce: "0123456789abcdef", SubmittedAt: "2026-10-22T10:00:00Z", PayloadSHA256: submission.PayloadSHA256(compact), Payload: compact}
	s.write()
	s.record("submission_accepted", map[string]any{"attempt": 1, "sha256": s.file.PayloadSHA256})
	if applied {
		s.record(fix.LedgerApply, fix.ApplyResult{Written: 2})
	}
	return s
}

func kindOf(kind jobs.Kind, dir string) submission.Kind {
	if kind == jobs.KindTriage {
		return triage.Kind(dir)
	}
	return fix.FixReport
}

// write puts the file of the submission in the run directory.
func (s *submittedRun) write() {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.run.Path, "submission.json"), submission.Marshal(s.file), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *submittedRun) record(kind string, data any) {
	s.t.Helper()
	if err := s.run.Record(kind, data); err != nil {
		s.t.Fatal(err)
	}
}

// stage is what the submission of the run allows to be skipped.
func (s *submittedRun) stage() (string, string) {
	return skipStage(context.Background(), s.kind, s.clone, s.run.ID)
}

// TestTheBrainIsSkippedOnlyWhenEverySubmissionConditionHolds_TS_14_17 verifies TS-14-17: a submission of
// the kind of the run, for this run, whose payload is what its hash says, that the host accepted last and
// that still passes the check of its kind against the tree, and, for a fix report, that the tree was
// applied after; and that each condition broken makes the brain run.
// Verifies: 14-REQ-3.1
func TestTheBrainIsSkippedOnlyWhenEverySubmissionConditionHolds_TS_14_17(t *testing.T) {
	files := map[string]string{"src/greet.sh": "echo hello\n", "tests/hello_test.sh": helloTest}
	const report = `{"subject":"say hello","root_cause":"x","change":"y","files":[{"path":"src/greet.sh","why":"w"}],"tests":[{"path":"tests/hello_test.sh","proves":"p"}]}`
	const question = `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`
	fixRun := func(t *testing.T) *submittedRun {
		return newSubmittedRun(t, jobs.KindFix, files, fix.KindName, report, true)
	}

	if s := fixRun(t); true {
		if stage, why := s.stage(); stage != stageApplied {
			t.Fatalf("a submission that holds every condition: stage %q (%s), want %s", stage, why, stageApplied)
		}
	}
	for name, break_ := range map[string]func(s *submittedRun){
		"the kind of another run": func(s *submittedRun) { s.file.Kind = triage.KindName; s.write() },
		"another run":             func(s *submittedRun) { s.file.RunID = "20200101_000000_aaaaaa"; s.write() },
		"a payload that is not what its hash says": func(s *submittedRun) {
			// The ledger says the host accepted the hash that the file carries: only the payload disagrees.
			s.file.PayloadSHA256 = submission.PayloadSHA256([]byte(`{}`))
			s.write()
			s.record("submission_accepted", map[string]any{"attempt": 2, "sha256": s.file.PayloadSHA256})
			s.record(fix.LedgerApply, fix.ApplyResult{Written: 1})
		},
		"a later submission that the host accepted": func(s *submittedRun) {
			s.record("submission_accepted", map[string]any{"attempt": 2, "sha256": submission.PayloadSHA256([]byte(`{"other":true}`))})
			s.record(fix.LedgerApply, fix.ApplyResult{Written: 1})
		},
		"no record that the host accepted it": func(s *submittedRun) {
			if err := os.Remove(filepath.Join(s.run.Path, "ledger.jsonl")); err != nil {
				s.t.Fatal(err)
			}
		},
		"a payload that no longer passes its check": func(s *submittedRun) {
			s.file.Payload = json.RawMessage(`{"subject":"s"}`)
			s.file.PayloadSHA256 = submission.PayloadSHA256(s.file.Payload)
			s.write()
			s.record("submission_accepted", map[string]any{"attempt": 3, "sha256": s.file.PayloadSHA256})
			s.record(fix.LedgerApply, fix.ApplyResult{Written: 1})
		},
		"a test that is gone from the tree": func(s *submittedRun) {
			if err := os.Remove(filepath.Join(s.clone, "tests", "hello_test.sh")); err != nil {
				s.t.Fatal(err)
			}
		},
		"no submission file": func(s *submittedRun) {
			if err := os.Remove(filepath.Join(s.run.Path, "submission.json")); err != nil {
				s.t.Fatal(err)
			}
		},
		"a file that is no JSON": func(s *submittedRun) {
			if err := os.WriteFile(filepath.Join(s.run.Path, "submission.json"), []byte("{"), 0o644); err != nil {
				s.t.Fatal(err)
			}
		},
	} {
		t.Run("a fix report with "+name, func(t *testing.T) {
			s := fixRun(t)
			break_(s)
			if stage, why := s.stage(); stage != "" {
				t.Errorf("stage %q, want the brain to run (%s)", stage, why)
			}
		})
	}

	t.Run("a fix report that was accepted and not applied", func(t *testing.T) {
		s := newSubmittedRun(t, jobs.KindFix, files, fix.KindName, report, false)
		if stage, _ := s.stage(); stage != "" {
			t.Errorf("stage %q, want the brain to run", stage)
		}
	})
	t.Run("a fix report that was applied before it was accepted", func(t *testing.T) {
		s := newSubmittedRun(t, jobs.KindFix, files, fix.KindName, report, false)
		s.record(fix.LedgerApply, fix.ApplyResult{Written: 1}) // after the acceptance: now it was applied
		if err := os.Remove(filepath.Join(s.run.Path, "ledger.jsonl")); err != nil {
			t.Fatal(err)
		}
		s.record(fix.LedgerApply, fix.ApplyResult{Written: 1})
		s.record("submission_accepted", map[string]any{"attempt": 1, "sha256": s.file.PayloadSHA256})
		if stage, _ := s.stage(); stage != "" {
			t.Errorf("stage %q, want the brain to run: the apply is of an earlier round", stage)
		}
	})
	t.Run("a fix question needs no apply", func(t *testing.T) {
		s := newSubmittedRun(t, jobs.KindFix, files, fix.KindName, question, false)
		if stage, why := s.stage(); stage != stageSubmitted {
			t.Errorf("stage %q (%s), want %s", stage, why, stageSubmitted)
		}
	})
	t.Run("a triage diagnosis is checked with the validation of triage", func(t *testing.T) {
		repo := map[string]string{"main.go": triageFiles["main.go"], "util/util.go": triageFiles["util/util.go"], "go.mod": triageFiles["go.mod"]}
		s := newSubmittedRun(t, jobs.KindTriage, repo, triage.KindName, probableDiagnosis, false)
		if stage, why := s.stage(); stage != stageSubmitted {
			t.Fatalf("stage %q (%s), want %s", stage, why, stageSubmitted)
		}
		if err := os.Remove(filepath.Join(s.clone, "util", "util.go")); err != nil { // a file the diagnosis cites is gone
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", s.clone, "rm", "-q", "--cached", "util/util.go").CombinedOutput(); err != nil {
			t.Fatalf("git rm: %v\n%s", err, out)
		}
		if stage, _ := s.stage(); stage != "" {
			t.Errorf("stage %q, want the brain to run: the diagnosis cites a file that is not in the tree", stage)
		}
	})
}
