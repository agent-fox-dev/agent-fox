package afcmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// runCtx runs the app with a context of the test's own.
func (e *fixEnv) runCtx(ctx context.Context, stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := e.app.Main(ctx, append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, oneDocument(e.t, out.String())
}

// spyRound is a round of a spy brain that writes the files into its box, submits the payload
// and has used the cost and the turns.
func spyRound(t *testing.T, files map[string]string, payload string, cost float64, turns int) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		for name, content := range files {
			path := filepath.Join(b.Box.Spec().Workdir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Error(err)
			}
		}
		out, err := submitting(payload)(ctx, b)
		out.CostUSD, out.Turns, out.CostSource = cost, turns, brain.CostMeasured
		return out, err
	}
}

// spyRounds makes the spy brain do the rounds in turn, and end with nothing past the last.
func spyRounds(t *testing.T, rounds ...func(context.Context, brain.Brief) (brain.Outcome, error)) *spyBrain {
	t.Helper()
	s := &spyBrain{}
	n := 0
	s.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		i := n
		n++
		if i < len(rounds) {
			return rounds[i](ctx, b)
		}
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}
	useFixSpy(t, s)
	return s
}

// The edits of the scenarios: a change the fixture gate does not take, one it does, and one
// that proves itself with a test of its own.
var (
	spyBroken = map[string]string{"src/greet.sh": ":\n"}
	spyFixed  = map[string]string{"src/greet.sh": "echo hello\n", "tests/hello_test.sh": helloTest}
)

const spyReport = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"},{"path":"tests/hello_test.sh","why":"the proof"}]}`

// lifeboxes registers a box provider that is the local one, and counts the boxes it opens and
// the boxes that are closed.
func lifeboxes(t *testing.T) (opened, closed *atomic.Int32) {
	t.Helper()
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	opened, closed = new(atomic.Int32), new(atomic.Int32)
	entry := local
	entry.Name = "lifebox"
	entry.New = func() sandbox.Provider { return lifeProvider{Provider: local.New(), opened: opened, closed: closed} }
	box.Default.Register(entry)
	return opened, closed
}

type lifeProvider struct {
	sandbox.Provider
	opened, closed *atomic.Int32
}

func (p lifeProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	p.opened.Add(1)
	return lifeSandbox{Sandbox: sb, closed: p.closed}, nil
}

type lifeSandbox struct {
	sandbox.Sandbox
	closed *atomic.Int32
}

func (s lifeSandbox) Close(ctx context.Context) error {
	s.closed.Add(1)
	return s.Sandbox.Close(ctx)
}

// fixOnBase asserts the checkout is on the base branch with a clean tree.
func (e *fixEnv) onBase(t *testing.T) {
	t.Helper()
	if now := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "--abbrev-ref", "HEAD")); now != "main" {
		t.Errorf("checked out %q, want the base", now)
	}
	if status := gitDo(t, e.dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("git status = %q, want a clean tree", status)
	}
}

// remoteBranches are the branches of the origin of the checkout.
func (e *fixEnv) remoteBranches(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(gitDo(t, e.dir, "ls-remote", "--heads", "origin")), "\n") {
		if _, ref, ok := strings.Cut(line, "refs/heads/"); ok {
			names = append(names, ref)
		}
	}
	return names
}

// effects are the side effects of an envelope, as kind to state.
func effects(env map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range env["side_effects"].([]any) {
		m := s.(map[string]any)
		out[m["kind"].(string)] = m["state"].(string)
	}
	return out
}

func artifactKinds(env map[string]any) []string {
	var kinds []string
	for _, a := range env["artifacts"].([]any) {
		kinds = append(kinds, a.(map[string]any)["kind"].(string))
	}
	return kinds
}

// TestALandableChangeLandsThroughTheLoop guards the whole way of 09-PATH-1 at the command: a
// brain that fixes the issue gets a branch with one commit that closes it, pushed, a pull
// request, exit 0, and the checkout back on the base.
func TestALandableChangeLandsThroughTheLoop(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyFixed, spyReport, 0.5, 4))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	if res["outcome"] != "landed" || res["closes"] != true || res["landable"] != true || res["pushed"] != true || res["rounds"] != float64(1) || res["attempts"] != float64(1) || res["attempts_max"] != float64(2) || res["land"] != "pr" {
		t.Errorf("result = %v", res)
	}
	if !strings.HasPrefix(branch, "fix/7-") || !slices.Contains(e.remoteBranches(t), branch) {
		t.Errorf("branch %q, remote %v", branch, e.remoteBranches(t))
	}
	msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch)
	if !strings.Contains(msg, "Closes "+e.forge.IssueURL(7)) || !strings.Contains(msg, "Agent-Fox-Run: "+env["run_id"].(string)) {
		t.Errorf("message = %q", msg)
	}
	if n := strings.Count(strings.TrimSpace(gitDo(t, e.dir, "rev-list", resultOf(t, env)["base_head"].(string)+".."+branch)), "\n") + 1; n != 1 {
		t.Errorf("%d commits above the base, want one", n)
	}
	if prs := e.forge.PullRequests(); len(prs) != 1 || prs[0].Draft || prs[0].Head != branch || !strings.Contains(prs[0].Body, "brain `spy` · cost $0.5000 (measured)") {
		t.Errorf("pull requests = %+v, want one that is ready for review and whose footer says what the run used", prs)
	}
	e.onBase(t)
	if u, _ := env["usage"].(map[string]any); u["cost_usd"] != 0.5 || u["turns"] != float64(4) || u["brain"] != "spy" {
		t.Errorf("usage = %v, want what the round used", env["usage"])
	}
}

// TestWithNoAttemptLeftTheAppliedChangeIsParkedAndNothingAppliedIsAFailure_TS_09_50 verifies
// TS-09-50.
// Verifies: 09-REQ-7.5
func TestWithNoAttemptLeftTheAppliedChangeIsParkedAndNothingAppliedIsAFailure_TS_09_50(t *testing.T) {
	requireMake(t)

	t.Run("a red change and a round that failed", func(t *testing.T) {
		e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
		spyRounds(t, spyRound(t, spyBroken, spyReport, 10, 4)) // spends the whole attempt; attempt 2 comes to nothing

		code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--land", "branch")

		if code != 4 || env["status"] != "unverified" {
			t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
		}
		res := resultOf(t, env)
		branch, _ := res["branch"].(string)
		if res["outcome"] != "parked" || res["verdict"] != "still_failing" || res["attempts"] != float64(2) || res["rounds"] != float64(2) {
			t.Errorf("result = %v", res)
		}
		if subject := strings.TrimSpace(gitDo(t, e.dir, "log", "-1", "--format=%s", branch)); subject != "wip: say hello" {
			t.Errorf("subject = %q, want the subject of the last accepted report", subject)
		}
		if codes := warningCodes(env); !slices.Contains(codes, "attempt_failed") || !slices.Contains(codes, "fix_not_landed") {
			t.Errorf("warnings = %v", codes)
		}
		if msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Parked by af fix: verdict still_failing\nFailing checks:\n- make check\n") {
			t.Errorf("message = %q, want the verdict and the failing check", msg)
		}
		if !slices.Contains(e.remoteBranches(t), branch) || res["pushed"] != true || e.forge.Count(fakeforge.OpPullRequest) != 0 {
			t.Errorf("remote %v, pushed %v: --land branch pushes the branch and opens nothing", e.remoteBranches(t), res["pushed"])
		}
		e.onBase(t)
	})
	t.Run("every round failed before anything was applied", func(t *testing.T) {
		e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
		spyRounds(t, func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{}, &toolio.Failure{Category: toolio.CategoryAuth, Message: "the key was refused"}
		})

		code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local")

		errObj := refusedBy(t, code, env, 1, "auth")
		if env["status"] != "failed" || !strings.Contains(errObj["message"].(string), "the key was refused") {
			t.Errorf("status %v, error %v, want the failure of the last round", env["status"], errObj)
		}
		if !slices.Equal(e.branches(), []string{"main"}) {
			t.Errorf("branches = %v", e.branches())
		}
		e.onBase(t)
	})
}

// TestACancelledRunKeepsItsWorkOnALocalBranch_TS_09_51 verifies TS-09-51: a run that is
// cancelled with a change applied parks it on a local branch, pushes nothing and writes nothing
// to the forge, and is told the run was cancelled; under --dry-run the change is discarded.
// Verifies: 09-REQ-7.6
func TestACancelledRunKeepsItsWorkOnALocalBranch_TS_09_51(t *testing.T) {
	requireMake(t)
	for _, tc := range []struct {
		name string
		args []string
	}{{"a run", nil}, {"a dry run", []string{"--dry-run"}}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			head := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			spyRounds(t,
				spyRound(t, spyBroken, spyReport, 1, 1),
				func(context.Context, brain.Brief) (brain.Outcome, error) {
					cancel() // the run is stopped while the second round runs
					return brain.Outcome{StopReason: brain.StopEnded}, nil
				})

			code, env := e.runCtx(ctx, "", append([]string{"fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--sandbox", "local"}, tc.args...)...)

			errObj := refusedBy(t, code, env, 1, "cancelled")
			_ = errObj
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
			if len(e.remoteBranches(t)) != 1 {
				t.Errorf("remote branches = %v: nothing is pushed", e.remoteBranches(t))
			}
			e.onBase(t)
			if tc.args == nil {
				local := e.branches()
				if len(local) != 2 {
					t.Fatalf("branches = %v, want the base and the branch the change is parked on", local)
				}
				for _, b := range local {
					if b != "main" {
						if subject := strings.TrimSpace(gitDo(t, e.dir, "log", "-1", "--format=%s", b)); !strings.HasPrefix(subject, "wip: ") {
							t.Errorf("subject = %q", subject)
						}
					}
				}
			} else if !slices.Equal(e.branches(), []string{"main"}) || strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head {
				t.Errorf("a dry run left branches %v", e.branches())
			}
		})
	}
}

// TestADeadlineThatPassesWhileTheChangeIsMeasuredKeepsItLocally guards 09-REQ-7.6 for the
// deadline: the run is told it timed out, and the change is on a local branch.
func TestADeadlineThatPassesWhileTheChangeIsMeasuredKeepsItLocally(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	// The baseline is quick; the gate on the change waits past the deadline, in the box.
	slow := map[string]string{"src/greet.sh": "sleep 60; echo hello\n"}
	spyRounds(t, spyRound(t, slow, spyReport, 0, 1))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--timeout", "8s")

	refusedBy(t, code, env, 1, "timeout")
	if len(e.branches()) != 2 || len(e.remoteBranches(t)) != 1 || len(e.forge.Writes()) != 0 {
		t.Errorf("branches %v, remote %v, writes %d: the change is parked here and nowhere else", e.branches(), e.remoteBranches(t), len(e.forge.Writes()))
	}
	e.onBase(t)
}

// TestOneBoxIsHeldAndClosedOnEveryWayOutAndUsageIsTheSumOfTheRounds_TS_09_52 verifies
// TS-09-52.
// Verifies: 09-REQ-7.7
func TestOneBoxIsHeldAndClosedOnEveryWayOutAndUsageIsTheSumOfTheRounds_TS_09_52(t *testing.T) {
	requireMake(t)
	opened, closed := lifeboxes(t)
	check := func(name string, wantOpened int32) {
		t.Helper()
		if opened.Load() != wantOpened || closed.Load() != wantOpened {
			t.Errorf("%s: %d boxes opened and %d closed, want %d of each", name, opened.Load(), closed.Load(), wantOpened)
		}
	}

	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyBroken, spyReport, 1.25, 3), spyRound(t, spyBroken, spyReport, 2.5, 5), spyRound(t, spyFixed, spyReport, 0.25, 7))
	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "lifebox")
	if code != 0 || resultOf(t, env)["rounds"] != float64(3) {
		t.Fatalf("exit code %d, result %v, error %v", code, env["result"], env["error"])
	}
	check("a run of three rounds", 1)
	if u, _ := env["usage"].(map[string]any); u["cost_usd"] != 4.0 || u["turns"] != float64(15) || u["cost_source"] != "measured" {
		t.Errorf("usage = %v, want the sum of the three rounds", env["usage"])
	}

	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t)
	if code, _ := e2.fix(e2.forge.IssueURL(7), "--brain", "spy", "--sandbox", "lifebox"); code != 1 {
		t.Errorf("a run whose rounds fail: exit code %d", code)
	}
	check("a run that failed", 2)

	e3 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spyRounds(t, func(context.Context, brain.Brief) (brain.Outcome, error) {
		cancel()
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	})
	e3.runCtx(ctx, "", "fix", e3.forge.IssueURL(7), "--dir", e3.dir, "--brain", "spy", "--sandbox", "lifebox")
	check("a run that was cancelled", 3)
}

// TestADryRunDiscardsTheChangeAndPlansTheWrites_TS_09_65 verifies TS-09-65.
// Verifies: 09-REQ-9.5
func TestADryRunDiscardsTheChangeAndPlansTheWrites_TS_09_65(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	head := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
	spy := spyRounds(t, spyRound(t, spyFixed, spyReport, 0.5, 4))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--dry-run")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v: a dry run has the status the real run would have had", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	if res["outcome"] != "dry_run" || res["landable"] != true || res["closes"] != true || res["branch"] != nil || res["commit"] != nil || res["pushed"] != false || res["pull_request"] != nil {
		t.Errorf("result = %v", res)
	}
	if !slices.Equal(e.branches(), []string{"main"}) || strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head {
		t.Errorf("branches %v: a dry run makes no branch and no commit", e.branches())
	}
	e.onBase(t)
	states := effects(env)
	for _, kind := range []string{"branch", "commit", "push", "create_pull_request"} {
		if states[kind] != "planned" {
			t.Errorf("side effects = %v, want %s planned", states, kind)
		}
	}
	if n := len(e.forge.Writes()); n != 0 || len(e.remoteBranches(t)) != 1 {
		t.Errorf("%d writes to the forge, remote branches %v", n, e.remoteBranches(t))
	}
	if spy.runs != 1 {
		t.Errorf("the brain ran %d times: a dry run still asks it", spy.runs)
	}

	// The same run for a text under --no-verify has the status of that run: unverified.
	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))
	code, env = e2.fix("the greeting says helo", "--repo", "o/r", "--brain", "spy", "--sandbox", "local", "--dry-run", "--no-verify")
	if code != 4 || env["status"] != "unverified" || resultOf(t, env)["outcome"] != "dry_run" {
		t.Fatalf("exit code %d, status %v, result %v, error %v", code, env["status"], env["result"], env["error"])
	}
	if got := effects(env); got["create_issue"] != "planned" || got["create_pull_request"] != "planned" || got["branch"] != "planned" {
		t.Errorf("side effects = %v, want the issue, the branch and the pull request planned", got)
	}
	e2.onBase(t)
}

// TestEveryEndOfARunLeavesTheBaseCheckedOutClean_TS_09_66 verifies TS-09-66 over the ends a
// run can have and the modes it can land in.
// Verifies: 09-REQ-9.6
func TestEveryEndOfARunLeavesTheBaseCheckedOutClean_TS_09_66(t *testing.T) {
	requireMake(t)
	question := `{"question":{"text":"Which?","options":["x","y"]}}`
	scenarios := map[string]struct {
		rounds []func(context.Context, brain.Brief) (brain.Outcome, error)
		args   []string
		code   int
		onHead bool
	}{
		"landed":      {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyFixed, spyReport, 0, 1)}, nil, 0, false},
		"parked":      {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyBroken, spyReport, 10, 1)}, []string{"--attempts", "1"}, 4, false},
		"needs_human": {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyBroken, question, 0, 1)}, nil, 3, true},
		"dry_run":     {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyFixed, spyReport, 0, 1)}, []string{"--dry-run"}, 0, true},
		// A question in a later round: the change of the round before it, which was refused, is not kept.
		"needs_human after a refused round":         {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyBroken, spyReport, 0, 1), spyRound(t, nil, question, 0, 1)}, nil, 3, true},
		"dry_run needs_human after a refused round": {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyBroken, spyReport, 0, 1), spyRound(t, nil, question, 0, 1)}, []string{"--dry-run"}, 3, true},
		"dry_run parked":                            {[]func(context.Context, brain.Brief) (brain.Outcome, error){spyRound(t, spyBroken, spyReport, 10, 1)}, []string{"--dry-run", "--attempts", "1"}, 4, true},
	}
	for name, sc := range scenarios {
		for _, mode := range []string{"pr", "branch", "none"} {
			t.Run(name+" "+mode, func(t *testing.T) {
				e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
				head := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
				spyRounds(t, sc.rounds...)

				code, env := e.fix(e.forge.IssueURL(7), append([]string{"--brain", "spy", "--sandbox", "local", "--land", mode}, sc.args...)...)

				if code != sc.code {
					t.Fatalf("exit code %d (error %v), want %d", code, env["error"], sc.code)
				}
				e.onBase(t)
				if sc.onHead && strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head {
					t.Error("HEAD moved: a run that ends here makes no commit")
				}
				if sc.onHead && !slices.Equal(e.branches(), []string{"main"}) {
					t.Errorf("branches = %v: a run that ends here makes no branch", e.branches())
				}
				if mode == "none" && (len(e.forge.Writes()) != 0 || len(e.remoteBranches(t)) != 1) {
					t.Errorf("--land none wrote to the forge (%d) or pushed (%v)", len(e.forge.Writes()), e.remoteBranches(t))
				}
			})
		}
	}
}

// TestArtifactsAndSideEffectsAreReportedAsDonePlannedOrFailed_TS_09_68 verifies TS-09-68.
// Verifies: 09-REQ-10.2
func TestArtifactsAndSideEffectsAreReportedAsDonePlannedOrFailed_TS_09_68(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))
	_, landed := e.fix("the greeting says helo", "--repo", "o/r", "--brain", "spy", "--sandbox", "local")

	kinds := artifactKinds(landed)
	for _, want := range []string{"branch", "commit", "pull_request", "issue", "report", "submission", "run_dir"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("artifacts = %v, want %s", kinds, want)
		}
	}
	for _, kind := range []string{"branch", "commit", "push", "create_issue", "create_pull_request"} {
		if effects(landed)[kind] != "done" {
			t.Errorf("side effects = %v, want %s done", effects(landed), kind)
		}
	}
	if got := effectTargets(landed); got["create_issue"] != "o/r" || got["create_pull_request"] != "o/r" {
		t.Errorf("side effect targets = %v, want the repository of the run", got)
	}

	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))
	_, dry := e2.fix("the greeting says helo", "--repo", "o/r", "--brain", "spy", "--sandbox", "local", "--dry-run")
	if got := effects(dry); got["branch"] != "planned" || got["commit"] != "planned" || got["push"] != "planned" || got["create_issue"] != "planned" {
		t.Errorf("a dry run: side effects = %v", got)
	}

	e3 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))
	if err := os.WriteFile(filepath.Join(e3.origin, "hooks", "pre-receive"), []byte("#!/bin/sh\necho 'rejected by the test' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, pushFailed := e3.fix(e3.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local")
	refusedBy(t, code, pushFailed, 1, "git")
	if effects(pushFailed)["push"] != "failed" || effects(pushFailed)["commit"] != "done" {
		t.Errorf("a push that failed: side effects = %v", effects(pushFailed))
	}
	var hint bool
	for _, n := range pushFailed["next"].([]any) {
		argv := n.(map[string]any)["argv"].([]any)
		hint = hint || (len(argv) == 5 && argv[0] == "git" && argv[1] == "push" && argv[3] == "origin")
	}
	if !hint {
		t.Errorf("next = %v, want the push to try again", pushFailed["next"])
	}
	e3.onBase(t)
}

// TestTheDefaultsOfTheLoopApplyWhenFlagsAreOmitted_TS_09_2 verifies TS-09-2 for the result and
// the bounds the brain gets.
// Verifies: 09-REQ-1.2
func TestTheDefaultsOfTheLoopApplyWhenFlagsAreOmitted_TS_09_2(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spy := spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))

	_, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--dry-run")

	res := resultOf(t, env)
	if res["attempts_max"] != float64(2) || res["land"] != "pr" {
		t.Errorf("result = %v", res)
	}
	if b := spy.brief; b == nil || b.MaxCostUSD != 10 || b.MaxTurns != 150 {
		t.Errorf("brief = %+v, want the default budget and turns", b)
	}
	if bx, _ := res["box"].(map[string]any); bx["provider"] != "local" {
		t.Errorf("box = %v, want the default sandbox", res["box"])
	}

	// The default brain is the agentkit driver: with no credential for its model, it is the one that
	// cannot be made, and the run is refused before a box is opened.
	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	code, env := e2.fix(e2.forge.IssueURL(7), "--dry-run")
	errObj := refusedBy(t, code, env, 1, "auth")
	if hint, _ := errObj["fix_hint"].(map[string]any); hint["env"] != "ANTHROPIC_API_KEY" {
		t.Errorf("error = %v, want the agentkit brain, which needs ANTHROPIC_API_KEY", errObj)
	}
}

// TestEachFixCommandAcceptsExactlyItsFlags_TS_09_3 verifies TS-09-3: the flags each command
// shows in its help, and the usage failure of one it does not take.
// Verifies: 09-REQ-1.3
func TestEachFixCommandAcceptsExactlyItsFlags_TS_09_3(t *testing.T) {
	e := newFixEnv(t, fakeforge.GitHub)
	flagsOf := func(path ...string) []string {
		var out strings.Builder
		e.app.Main(context.Background(), append([]string{"af", "help"}, path...), strings.NewReader(""), &out, &out)
		var flags []string
		for _, m := range regexp.MustCompile(`(?m)^\s+--([a-z][a-z-]*)`).FindAllStringSubmatch(out.String(), -1) {
			flags = append(flags, m[1])
		}
		slices.Sort(flags)
		return slices.Compact(flags)
	}
	brainFlags := []string{"brain", "sandbox", "budget", "max-turns", "script"}
	common := []string{"dir", "timeout"}
	want := map[string][]string{
		"fix":     append(append(slices.Clone(common), "base", "attempts", "land", "no-verify", "repo", "dry-run", "context"), brainFlags...),
		"fix run": append(append(slices.Clone(common), "run-id"), brainFlags...),
	}
	for command, flags := range want {
		slices.Sort(flags)
		got := flagsOf(strings.Fields(command)...)
		for _, f := range flags {
			if !slices.Contains(got, f) {
				t.Errorf("af help %s does not show --%s: %v", command, f, got)
			}
		}
		for _, f := range got {
			if !slices.Contains(flags, f) && f != "emit-events" && f != "output" {
				t.Errorf("af help %s shows --%s, which it does not take", command, f)
			}
		}
	}
	for _, args := range [][]string{{"fix", "run", "--run-id", "x", "--base", "main"}, {"fix", "run", "--run-id", "x", "--attempts", "2"}} {
		if code, env := e.run("", args...); code != 2 {
			t.Errorf("%v: exit code %d (%v), want usage", args, code, env["error"])
		}
	}
}

// TestTheFixCommandsHaveRealResultSchemas_TS_09_1 verifies TS-09-1 as it stands: the schemas
// of af fix and af fix run are in af --schema, each describing something, and the golden file
// has them.
// Verifies: 09-REQ-1.1
func TestTheFixCommandsHaveRealResultSchemas_TS_09_1(t *testing.T) {
	var out bytes.Buffer
	newApp().Main(context.Background(), []string{"af", "--schema"}, strings.NewReader(""), &out, &bytes.Buffer{})
	doc := oneDocument(t, out.String())
	defs, _ := doc["$defs"].(map[string]any)
	golden, err := os.ReadFile(filepath.Join("..", "..", "cmd", "af", "testdata", "schema.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fix", "fix_run"} {
		schema, _ := defs[name].(map[string]any)
		if props, _ := schema["properties"].(map[string]any); len(props) < 3 {
			t.Errorf("%q: result schema = %v, want one that describes the result", name, schema)
		}
		if !strings.Contains(string(golden), "\""+name+"\": {") {
			t.Errorf("the golden file lacks the schema of %s", name)
		}
	}
	if schema, _ := defs["fix"].(map[string]any); schema["properties"].(map[string]any)["outcome"] == nil {
		t.Errorf("the schema of fix is still the placeholder: %v", schema)
	}
}

// effectTargets are the side effects of an envelope, as kind to target.
func effectTargets(env map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range env["side_effects"].([]any) {
		m := s.(map[string]any)
		out[m["kind"].(string)] = m["target"].(string)
	}
	return out
}

// TestADryRunPlansTheWritesThatTheModeWouldMake guards what a dry run lists: the branch it
// would make, named as the real run names it, and a push and a pull request only where the
// mode asks for them.
func TestADryRunPlansTheWritesThatTheModeWouldMake(t *testing.T) {
	requireMake(t)
	for mode, want := range map[string]struct {
		push, pr bool
	}{"pr": {true, true}, "branch": {true, false}, "none": {false, false}} {
		t.Run(mode, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))

			code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--dry-run", "--land", mode)

			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			states, targets := effects(env), effectTargets(env)
			if states["branch"] != "planned" || states["commit"] != "planned" || targets["branch"] != "fix/7-greeting-wrong" {
				t.Errorf("side effects = %v %v, want the branch of the issue and the commit planned", states, targets)
			}
			if (states["push"] == "planned") != want.push || (states["create_pull_request"] == "planned") != want.pr {
				t.Errorf("side effects = %v, want push %v and pull request %v", states, want.push, want.pr)
			}
			if want.push && targets["push"] != "origin/fix/7-greeting-wrong" {
				t.Errorf("push target = %q", targets["push"])
			}
			if _, has := states["create_issue"]; has {
				t.Errorf("side effects = %v: an issue is not made for an issue", states)
			}
		})
	}

	// A dry run that would park plans a draft pull request, and the parked branch.
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, spyRound(t, spyBroken, spyReport, 10, 1))
	_, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--dry-run", "--attempts", "1")
	if states := effects(env); states["create_pull_request"] != "planned" || states["branch"] != "planned" || env["status"] != "unverified" {
		t.Errorf("a dry run that would park: status %v, side effects %v", env["status"], states)
	}
}

// TestRoundsThatLeaveNothingToParkEndTheRunWithAFailure guards the end of a run whose rounds
// came to reports and no change: a brain that touches only protected paths changes nothing, and
// the run fails with the reason and not with a parking of nothing.
func TestRoundsThatLeaveNothingToParkEndTheRunWithAFailure(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	onlyProtected := map[string]string{".specs/note.md": "# a note\n"}
	spyRounds(t, spyRound(t, onlyProtected, spyReport, 10, 1), spyRound(t, onlyProtected, spyReport, 10, 1))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local")

	errObj := refusedBy(t, code, env, 1, "no_result")
	if !strings.Contains(errObj["message"].(string), "changed nothing") {
		t.Errorf("error = %v, want it to say that nothing changed", errObj)
	}
	if !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("branches = %v", e.branches())
	}
	e.onBase(t)
	if kinds := ledgerKinds(t, runDirOf(e, env)); count(kinds, "refusal") != 2 || count(kinds, "measure") != 2 || !slices.Contains(warningCodes(env), "protected_path_reverted") {
		t.Errorf("ledger kinds = %v, warnings %v, want two rounds, each refused for having changed nothing, and the protected path put back", kinds, warningCodes(env))
	}
}

func count(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// TestAFailureToMeasureLeavesTheWorkWhereItIsAndADryRunLeavesAMovedCheckoutAlone guards the
// errors that are neither a stop nor an end: a commit made outside af ends the run with the
// change in the tree, and a dry run does not reset a checkout that has moved.
func TestAFailureToMeasureLeavesTheWorkWhereItIsAndADryRunLeavesAMovedCheckoutAlone(t *testing.T) {
	requireMake(t)
	foreign := func(e *fixEnv) func(context.Context, brain.Brief) (brain.Outcome, error) {
		return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			e.writeFile("docs/other.md", "someone's\n")
			gitDo(t, e.dir, "add", "docs/other.md")
			gitDo(t, e.dir, "commit", "-q", "-m", "chore: made outside af", "docs/other.md")
			return spyRound(t, map[string]string{"docs/readme.md": "readme, changed\n"}, spyReport, 0, 1)(ctx, b)
		}
	}

	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, foreign(e))
	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local")
	if msg := refusedBy(t, code, env, 1, "git")["message"].(string); !strings.Contains(msg, "made outside af") {
		t.Errorf("error = %q", msg)
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "docs", "readme.md")); string(data) != "readme, changed\n" || !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("readme.md = %q, branches %v: the change stays in the tree and nothing is parked", data, e.branches())
	}

	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spyRounds(t, foreign(e2))
	code, env = e2.fix(e2.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--dry-run")
	refusedBy(t, code, env, 1, "git")
	if !slices.Contains(warningCodes(env), "dry_run_tree_kept") {
		t.Errorf("warnings = %v, want dry_run_tree_kept", warningCodes(env))
	}
	if log := gitDo(t, e2.dir, "log", "-1", "--format=%s"); !strings.Contains(log, "made outside af") {
		t.Errorf("the commit made outside af is gone: %q", log)
	}
}

// TestARunStoppedInItsFirstRoundHasNothingToKeep guards the stop before any report: there is no
// subject, no change and no branch, and no warning about a parking that was not needed.
func TestARunStoppedInItsFirstRoundHasNothingToKeep(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spyRounds(t, func(context.Context, brain.Brief) (brain.Outcome, error) {
		cancel()
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	})

	code, env := e.runCtx(ctx, "", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--sandbox", "local")

	refusedBy(t, code, env, 1, "cancelled")
	if !slices.Equal(e.branches(), []string{"main"}) || slices.Contains(warningCodes(env), "park_failed") {
		t.Errorf("branches %v, warnings %v", e.branches(), warningCodes(env))
	}
	e.onBase(t)
}

// TestAChangeThatCannotBeParkedAfterAStopIsToldAndStaysInTheTree guards the warning of a parking
// that fails: the work is not thrown away, and the run says where it is.
func TestAChangeThatCannotBeParkedAfterAStopIsToldAndStaysInTheTree(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spyRounds(t,
		func(c context.Context, b brain.Brief) (brain.Outcome, error) {
			gitDo(t, e.dir, "config", "--unset", "user.email") // the commit that parks cannot be made
			gitDo(t, e.dir, "config", "--unset", "user.name")
			gitDo(t, e.dir, "config", "user.useConfigOnly", "true")
			return spyRound(t, spyBroken, spyReport, 1, 1)(c, b)
		},
		func(context.Context, brain.Brief) (brain.Outcome, error) {
			cancel()
			return brain.Outcome{StopReason: brain.StopEnded}, nil
		})

	code, env := e.runCtx(ctx, "", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--sandbox", "local")

	refusedBy(t, code, env, 1, "cancelled")
	if !slices.Contains(warningCodes(env), "park_failed") {
		t.Errorf("warnings = %v, want park_failed", warningCodes(env))
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "src", "greet.sh")); string(data) != ":\n" || !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("greet.sh = %q, branches %v: the change stays in the working tree", data, e.branches())
	}
}

// TestABoxThatCannotBeOpenedFailsTheRunBeforeAnythingIsDone guards the first step of the loop.
func TestABoxThatCannotBeOpenedFailsTheRunBeforeAnythingIsDone(t *testing.T) {
	requireMake(t)
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	broken := local
	broken.Name = "brokenbox"
	broken.New = func() sandbox.Provider { return failingProvider{Provider: local.New()} }
	box.Default.Register(broken)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spy := spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "brokenbox")

	refusedBy(t, code, env, 1, "sandbox")
	if spy.runs != 0 || !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("the brain ran %d times, branches %v", spy.runs, e.branches())
	}
	e.onBase(t)
}

type failingProvider struct{ sandbox.Provider }

func (failingProvider) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, os.ErrPermission
}
