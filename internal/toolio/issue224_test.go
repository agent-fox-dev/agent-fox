package toolio

import (
	"io"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Issue #224 (1): the heartbeat's spend includes the phase still running, not
// only the phases already recorded.
func TestTheLiveSpendIncludesTheRunningPhase(t *testing.T) {
	run := NewRun("fix", "v1")
	run.AddPhase(PhaseInfo{Name: "analyse", CostUSD: 1.0})
	p := NewProgress(io.Discard, "fix", false, true)
	spend := liveSpend(run, p)

	p.PhaseStart("implement", "", 10, 5)
	p.Turn("implement", 1, agentrun.TurnUsage{CostUSD: 0.5})
	p.Turn("implement", 2, agentrun.TurnUsage{CostUSD: 0.25})
	if got := spend(); got != 1.75 {
		t.Errorf("spend during the phase = %v, want 1.75", got)
	}
	p.PhaseEnd("implement", "tool_use", 2, 0.75, 100, nil)
	run.AddPhase(PhaseInfo{Name: "implement", CostUSD: 0.75})
	if got := spend(); got != 1.75 {
		t.Errorf("spend after the phase was recorded = %v, want 1.75 (not counted twice)", got)
	}
	p.PhaseStart("review", "", 10, 5)
	if got := spend(); got != 1.75 {
		t.Errorf("spend at a new phase's start = %v, want 1.75", got)
	}

	s, clock, buf := heartbeatSink(t, spend)
	p.Turn("review", 1, agentrun.TurnUsage{CostUSD: 0.25})
	clock.Advance(15 * time.Second)
	hbs := eventsOfType(t, buf, "heartbeat")
	if len(hbs) != 1 || hbs[0]["cost_usd"] != 2.0 {
		t.Errorf("heartbeat = %v, want cost 2.0", hbs)
	}
	_ = s
}

// Issue #224 (2): a thread with more comments than were read is not a body
// cut at the byte bound: it neither claims the bound nor refuses --context.
func TestACommentCapIsNotTheByteBound(t *testing.T) {
	forge := &mockForgeClient{thread: issuex.IssueThread{
		Issue:     issuex.Issue{Number: 1, Title: "t", Body: "a short body"},
		Truncated: true,
	}}
	in, err := Resolve(t.Context(), "https://github.com/acme/widgets/issues/1", nil, forge, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Truncated || !in.CommentsTruncated {
		t.Fatalf("Truncated=%v CommentsTruncated=%v, want the comment cap only", in.Truncated, in.CommentsTruncated)
	}

	run := NewRun("fix", "v1")
	if err := checkResolved(in, 100, run); err != nil {
		t.Errorf("a 20-byte body with --context was refused: %v", err)
	}
	var codes []WarnCode
	for _, w := range run.Warnings() {
		codes = append(codes, w.Code)
	}
	if len(codes) != 1 || codes[0] != WarnCommentsTruncated {
		t.Errorf("warnings = %v, want only comments_truncated", codes)
	}

	// The byte bound itself still refuses with --context.
	big := Input{Kind: KindText, Body: strings.Repeat("x", MaxInputBytes), Truncated: true}
	if err := checkResolved(big, 10, NewRun("fix", "v1")); err == nil {
		t.Error("a body at the bound with --context was not refused")
	}
}

// Issue #224 (smaller): under --input-kind text or file the argument is not
// read as an issue URL, not even to pick the forge.
func TestTheForgeIsNotPickedFromAnArgumentForcedToText(t *testing.T) {
	url := "https://gitlab.com/acme/widgets/-/issues/3"
	if _, ok := forgeRepoFromArgument(url, ""); !ok {
		t.Error("a guessed issue URL does not pick its forge")
	}
	if _, ok := forgeRepoFromArgument(url, string(KindIssue)); !ok {
		t.Error("--input-kind issue does not pick the URL's forge")
	}
	for _, kind := range []string{string(KindText), string(KindFile), string(KindStdin)} {
		if _, ok := forgeRepoFromArgument(url, kind); ok {
			t.Errorf("--input-kind %s picked the forge from the argument", kind)
		}
	}
}

// Issue #224 (smaller): a negative ceiling is a usage error, like a negative
// --total-budget, not silently the default.
func TestANegativeCeilingIsAUsageError(t *testing.T) {
	for _, c := range []Common{{Budget: -1}, {MaxTurns: -1}, {Timeout: -time.Second}} {
		if err := c.ValidBounds(); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
	ok := Common{Budget: 2, MaxTurns: 10, Timeout: time.Minute}
	if err := ok.ValidBounds(); err != nil {
		t.Errorf("valid bounds refused: %v", err)
	}
}

type longSummary struct{ s string }

func (l longSummary) Summary() string { return l.s }

// Issue #224 (smaller): summary is capped at 200 characters, not bytes, never
// ends in a split rune, and the high-warning clause stays within the cap.
func TestTheSummaryIsCappedInCharacters(t *testing.T) {
	// "a" then two-byte runes puts byte 200 in the middle of a rune; a long
	// ASCII summary leaves no room for the clause within 200.
	for _, text := range []string{"a" + strings.Repeat("ü", 300), strings.Repeat("x", 250)} {
		run := NewRun("fix", "v1")
		run.Warn(WarnInputTruncated, "high", "x")
		env := run.Envelope(ExitOK, longSummary{text}, nil)
		if !utf8.ValidString(env.Summary) {
			t.Errorf("summary is not valid UTF-8: %q", env.Summary)
		}
		if n := utf8.RuneCountInString(env.Summary); n > 200 {
			t.Errorf("summary is %d characters, want at most 200", n)
		}
		if !strings.Contains(env.Summary, "high-severity warning") {
			t.Errorf("the high-warning clause was dropped: %q", env.Summary)
		}
	}
}

// selfReadingResult is a Result whose methods read the run, as a future one
// might.
type selfReadingResult struct{ run *Run }

func (r selfReadingResult) Summary() string {
	return strings.Repeat("w", len(r.run.Warnings())) + "done"
}

// Issue #224 (smaller): the envelope does not hold the run's lock while it
// calls the result's methods, so one that reads the run does not deadlock.
func TestAResultThatReadsTheRunDoesNotDeadlock(t *testing.T) {
	run := NewRun("fix", "v1")
	done := make(chan Envelope, 1)
	go func() { done <- run.Envelope(ExitOK, selfReadingResult{run}, nil) }()
	select {
	case env := <-done:
		if env.Summary != "done" {
			t.Errorf("summary = %q", env.Summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Envelope deadlocked on a result that reads the run")
	}
}

// Issue #224 (smaller): an event that cannot be encoded as it is still writes
// a line, so run_end stays the last event.
func TestAnUnencodableEventStillWritesALine(t *testing.T) {
	buf := &syncBuffer{}
	s := newEventsSink("fix", buf)
	s.Emit(newHeartbeatEvent("implement", 10, math.NaN()))
	s.Emit(newRunEndEvent("done", 0, ""))
	if hbs := eventsOfType(t, buf, "heartbeat"); len(hbs) != 1 {
		t.Errorf("heartbeat with a NaN cost: %d lines", len(hbs))
	}
	if ends := eventsOfType(t, buf, "run_end"); len(ends) != 1 {
		t.Errorf("run_end lines = %d", len(ends))
	}
}

// Issue #224 (smaller): the two shared warning codes carry a stage that is
// true for every tool that records them.
func TestSharedWarningsHaveAToolNeutralStage(t *testing.T) {
	for code, want := range map[WarnCode]string{WarnRepoMapBuildFailed: "repo_map", WarnToolErrors: "phase"} {
		if got := warnStages[code]; got != want {
			t.Errorf("%s stage = %q, want %q", code, got, want)
		}
	}
}

// Issue #222 (3): an issue URL whose forge cannot be told is the run's error,
// not a read from the workspace's forge.
func TestAnIssueURLWithAnUndecidableForgeIsAnError(t *testing.T) {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(k, "")
	}
	// Both API URLs name the host: it is either forge.
	t.Setenv("GITHUB_API_URL", "https://git.example.com/api/v3")
	t.Setenv("GITLAB_API_URL", "https://git.example.com/api/v4")
	if _, err := pickForge("https://git.example.com/team/app/issues/7", "", t.TempDir(), "test"); err == nil {
		t.Error("an undecidable forge fell back to the workspace's")
	}
	if f, err := pickForge("plain text", "", t.TempDir(), "test"); err != nil || f == nil {
		t.Errorf("a text input: %v, %v", f, err)
	}
}
