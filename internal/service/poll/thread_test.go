package poll

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"
)

func by(login, body string, at time.Time) issuex.Comment {
	return issuex.Comment{Body: body, User: issuex.User{Login: login}, CreatedAt: at}
}

func TestCounted_TheCommentsAfterTheAttemptsMarkerOrSinceItFinished(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	q := func(job, attempt string) string {
		return "Which?\n\n<!-- agent-fox job=" + job + " attempt=" + attempt + " kind=question -->"
	}
	for name, tc := range map[string]struct {
		thread   []issuex.Comment
		finished time.Time
		want     []string
	}{
		"after the first marker, in list order": {
			thread: []issuex.Comment{by("bob", "before", t0), by("af", q("job_1", "2"), t0), by("alice", "c2", t0.Add(3*time.Minute)),
				by("carol", "c3", t0.Add(2*time.Minute)), by("af", "reply "+q("job_1", "2"), t0)},
			finished: t0.Add(time.Hour), want: []string{"c2", "c3"},
		},
		"without a marker, from the second the attempt finished": {
			thread:   []issuex.Comment{by("bob", "before", t0.Add(-time.Second)), by("bob", "at", t0), by("bob", "after", t0.Add(time.Second))},
			finished: t0.Add(400 * time.Millisecond), want: []string{"at", "after"},
		},
		"the marker of another attempt or job is no anchor": {
			thread: []issuex.Comment{by("af", q("job_1", "12"), t0), by("af", q("job_9", "2"), t0), by("bob", "early", t0),
				by("bob", "late", t0.Add(time.Hour))},
			finished: t0.Add(time.Hour), want: []string{"late"},
		},
		"the service's own and blank comments never count": {
			thread: []issuex.Comment{by("af", q("job_1", "2"), t0), by("mallory", "see <!-- agent-fox:triage v1 --> this", t0),
				by("bob", " \n\t ", t0), by("alice", "valid", t0)},
			want: []string{"valid"},
		},
	} {
		var got []string
		for _, c := range counted(tc.thread, "job_1", 2, tc.finished) {
			got = append(got, c.Body)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: counted %q, want %q", name, got, tc.want)
		}
	}
}

func TestRender_EachCommentUnderItsAuthorWithNULAndInvalidUTF8Cleaned(t *testing.T) {
	got := render([]issuex.Comment{by("alice", "  use the\x00 greeting  ", time.Time{}), by("b\xffb", "and \xff B", time.Time{})})
	if want := "--- comment by alice ---\nuse the greeting\n\n--- comment by b�b ---\nand � B"; got != want {
		t.Errorf("render = %q, want %q", got, want)
	}
}

func TestBound_CutsALongTextAtARuneBoundaryWithTheCutLineLast(t *testing.T) {
	for name, text := range map[string]string{
		"8192 bytes":       strings.Repeat("a", 8192),
		"8193 bytes":       strings.Repeat("a", 8193),
		"four-byte runes":  "a" + strings.Repeat("😀", 2100),
		"two-byte runes":   strings.Repeat("é", 4500),
		"mixed with lines": strings.Repeat("ab€\n", 2100),
	} {
		got := bound(text)
		if len(text) <= maxThreadText {
			if got != text {
				t.Errorf("%s: a text within the limit was changed", name)
			}
			continue
		}
		kept, cut := strings.CutSuffix(got, "\n"+cutLine)
		if !cut || len(got) > maxThreadText || !utf8.ValidString(got) || !strings.HasPrefix(text, kept) || len(kept) < maxThreadText-len(cutLine)-4 {
			t.Errorf("%s: bound gave %d bytes ending %q, want at most 8192 valid bytes of the text and the cut line", name, len(got), got[len(got)-60:])
		}
	}
}
