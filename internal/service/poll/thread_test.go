package poll

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"
)

// by is a comment of the login with the body, made at the time.
func by(login, body string, at time.Time) issuex.Comment {
	return issuex.Comment{Body: body, User: issuex.User{Login: login}, CreatedAt: at}
}

// bodies are the bodies of the comments.
func bodies(cs []issuex.Comment) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

// TestTheCommentsThatCountFollowTheLatestAttemptsMarkerOrItsFinishTime_TS_19_16 verifies TS-19-16.
// Verifies: 19-REQ-3.2
func TestTheCommentsThatCountFollowTheLatestAttemptsMarkerOrItsFinishTime_TS_19_16(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	question := "Which greeting?\n\n<!-- agent-fox job=job_1 attempt=2 kind=question -->"
	t.Run("after the marker, in list order", func(t *testing.T) {
		thread := []issuex.Comment{
			by("bob", "c0", t0), by("agent-fox", question, t0.Add(time.Minute)),
			by("alice", "c2", t0.Add(3*time.Minute)), by("carol", "c3", t0.Add(2*time.Minute)), // list order, not time order
		}
		if got := bodies(counted(thread, "job_1", 2, t0.Add(time.Hour))); !slices.Equal(got, []string{"c2", "c3"}) {
			t.Errorf("counted %v, want [c2 c3]", got)
		}
	})
	t.Run("from the finish time, without the marker", func(t *testing.T) {
		at := t0.Add(10 * time.Minute)
		thread := []issuex.Comment{by("bob", "before", at.Add(-time.Second)), by("bob", "at", at), by("bob", "after", at.Add(time.Second))}
		if got := bodies(counted(thread, "job_1", 2, at)); !slices.Equal(got, []string{"at", "after"}) {
			t.Errorf("counted %v, want [at after]", got)
		}
	})
	t.Run("an older attempt's marker is no anchor", func(t *testing.T) {
		at := t0.Add(10 * time.Minute)
		thread := []issuex.Comment{
			by("agent-fox", "<!-- agent-fox job=job_1 attempt=1 kind=question -->", t0), by("bob", "early", t0.Add(time.Minute)),
			by("bob", "late", at),
		}
		if got := bodies(counted(thread, "job_1", 2, at)); !slices.Equal(got, []string{"late"}) {
			t.Errorf("counted %v, want [late]", got)
		}
		// Nor is attempt 12's for attempt 1, or another job's.
		thread = []issuex.Comment{
			by("agent-fox", "<!-- agent-fox job=job_1 attempt=12 kind=question -->", t0), by("agent-fox", "<!-- agent-fox job=job_9 attempt=1 kind=question -->", t0),
			by("bob", "x", t0),
		}
		if got := bodies(counted(thread, "job_1", 1, at)); len(got) != 0 {
			t.Errorf("counted %v, want none", got)
		}
	})
}

// TestCommentsBeforeTheQuestionMarkedAndBlankCommentsAreLeftOut_TS_19_17 verifies TS-19-17.
// Verifies: 19-REQ-3.3
func TestCommentsBeforeTheQuestionMarkedAndBlankCommentsAreLeftOut_TS_19_17(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	thread := []issuex.Comment{
		by("bob", "before the question", t0),
		by("agent-fox", "<!-- agent-fox job=job_1 attempt=1 kind=question -->", t0),
		by("mallory", "see this <!-- agent-fox:triage v1 run=x --> marker", t0),
		by("bob", " \n\t ", t0),
		by("alice", "valid", t0),
		by("agent-fox", "No answer found.\n\n<!-- agent-fox job=job_1 attempt=1 kind=no_answer -->", t0),
	}
	if got := bodies(counted(thread, "job_1", 1, t0)); !slices.Equal(got, []string{"valid"}) {
		t.Errorf("counted %v, want only the valid comment", got)
	}
}

// randomText is a text of runes of one to four bytes, NUL and invalid bytes among them.
func randomText(r *rand.Rand, n int) string {
	pieces := []string{"a", " ", "\n", "é", "€", "😀", "\x00", "\xff", "z\t"}
	var b strings.Builder
	for range n {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.String()
}

// TestTheRenderedTextIsValidUTF8WithNoNULInIssueOrder_TS_19_18 verifies TS-19-18, a property.
// Verifies: 19-REQ-3.4
func TestTheRenderedTextIsValidUTF8WithNoNULInIssueOrder_TS_19_18(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 18))
	for range 500 {
		var cs []issuex.Comment
		for i := range 1 + r.IntN(5) {
			cs = append(cs, by(randomText(r, 1+r.IntN(4))+string(rune('A'+i)), "x"+randomText(r, r.IntN(40))+"y", time.Time{}))
		}
		text := render(cs)
		if strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
			t.Fatalf("render(%+v) = %q: a NUL or invalid UTF-8", cs, text)
		}
		blocks := strings.Split(text, "\n\n--- comment by ")
		if len(blocks) != len(cs) || !strings.HasPrefix(text, "--- comment by ") {
			t.Fatalf("render(%+v) = %q: %d blocks, want %d", cs, text, len(blocks), len(cs))
		}
		for i, c := range cs {
			want := clean(c.User.Login) + " ---\n" + strings.TrimSpace(clean(c.Body))
			if got := strings.TrimPrefix(blocks[i], "--- comment by "); got != want {
				t.Fatalf("block %d is %q, want %q", i, got, want)
			}
		}
	}
	if got := render([]issuex.Comment{by("alice", "  use the greeting  ", time.Time{}), by("bob", "and B", time.Time{})}); got != "--- comment by alice ---\nuse the greeting\n\n--- comment by bob ---\nand B" {
		t.Errorf("render = %q", got)
	}
}

// clean is a text with NUL dropped and invalid UTF-8 replaced, as render cleans it.
func clean(s string) string { return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "�") }

// TestATextOver8192BytesIsCutAtARuneBoundaryWithTheCutLineLast_TS_19_19 verifies TS-19-19, a property.
// Verifies: 19-REQ-3.5
func TestATextOver8192BytesIsCutAtARuneBoundaryWithTheCutLineLast_TS_19_19(t *testing.T) {
	r := rand.New(rand.NewPCG(19, 19))
	runes := []string{"a", "é", "€", "😀", "\n"}
	for range 400 {
		var b strings.Builder
		for target := 8192 - 40 + r.IntN(400); b.Len() < target; {
			b.WriteString(runes[r.IntN(len(runes))])
		}
		text := b.String()
		got := bound(text)
		if len(text) <= 8192 {
			if got != text {
				t.Fatalf("a text of %d bytes was changed", len(text))
			}
			continue
		}
		lines := strings.Split(got, "\n")
		if len(got) > 8192 || !utf8.ValidString(got) || lines[len(lines)-1] != cutLine {
			t.Fatalf("bound of %d bytes gave %d bytes, valid %v, last line %q", len(text), len(got), utf8.ValidString(got), lines[len(lines)-1])
		}
		if kept := strings.TrimSuffix(got, "\n"+cutLine); !strings.HasPrefix(text, kept) {
			t.Fatal("the cut text is not a prefix of the text")
		}
	}
	if cutLine != "[… cut at 8192 bytes; the issue holds the rest …]" {
		t.Errorf("the cut line is %q", cutLine)
	}
}
