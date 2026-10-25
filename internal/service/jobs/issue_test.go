package jobs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

func TestParseIssue_AcceptsANumberAnIssueURLOrPathAndNumberAndNothingElse(t *testing.T) {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	accepted := map[string]int64{
		"1": 1, "42": 42, "9223372036854775807": 9223372036854775807,
		q("https://github.example.com/acme/widgets/issues/42"):                42,
		q("https://github.example.com/acme/widgets/issues/42#issuecomment-1"): 42,
		q("http://github.example.com/acme/widgets/issues/7"):                  7,
		q("https://gitlab.example.com/acme/widgets/-/issues/42"):              42,
		q("https://gitlab.example.com/group/sub/widgets/-/issues/9"):          9,
		q("acme/widgets#42"):     42,
		q("a/b#1"):               1,
		q("group/sub/widgets#7"): 7,
		q("a.b/c_d-e#1234"):      1234,
		q("acme/widgets#007"):    7,
		q("a/b" + strings.Repeat("a", 2042) + "#99"):                     99, // 2048 bytes
		q("https://gitlab.example.com/acme/widgets/-/issues/1?x=" + "y"): 1,
		q("https://github.example.com/acme/widgets/issues/5/"):           5,
		q("https://github.example.com/acme/widgets/issues/5#a/b#3"):      5,
		q("./.#3"): 3,
		q("https://github.example.com/acme/widgets/issues/9223372036854775807"): 9223372036854775807,
	}
	for raw, want := range accepted {
		if n, ok := jobs.ParseIssue(json.RawMessage(raw)); !ok || n != want {
			t.Errorf("ParseIssue(%.80s) = %d, %v, want %d", raw, n, ok, want)
		}
	}
	refused := []string{
		"0", "-1", "-0", "4.2", "42.0", "4e1", "1E2", "01", "+1", "9223372036854775808", "99999999999999999999",
		"null", "true", "[]", "{}", "[42]", `{"n":42}`, "", "nope",
		q("https://github.example.com/acme/widgets/pull/3"), q("https://github.example.com/acme/widgets/pull/3/files"),
		q("https://gitlab.example.com/acme/widgets/-/merge_requests/5"), q("https://example.com/acme/widgets/issues/42"),
		q("https://github.example.com/acme/widgets"), q("https://github.example.com/acme/widgets/issues/zero"),
		q("ftp://github.example.com/acme/widgets/issues/42"),
		q("42"), q(""), q("#42"), q("widgets#42"), q("acme/widgets"), q("acme/widgets#"), q("acme/widgets#0"), q("acme/widgets#-3"),
		q("acme/widgets#4x"), q("acme/widgets#42 "), q(" acme/widgets#42"), q("acme//widgets#42"), q("/acme/widgets#42"),
		q("acme/widgets/#42"), q("acme/wid gets#42"), q("acme/wid$gets#42"), q("acme/widgets#99999999999999999999"),
		q("a/b" + strings.Repeat("a", 2043) + "#99"), // a byte over 2048
		q(strings.Repeat("a/", 1100) + "x#5"),
	}
	for _, raw := range refused {
		if n, ok := jobs.ParseIssue(json.RawMessage(raw)); ok {
			t.Errorf("ParseIssue(%.80s) = %d, true, want it refused", raw, n)
		}
	}
}

// FuzzParseIssue holds for any bytes: ParseIssue does not panic, and accepts only JSON and only a positive number.
func FuzzParseIssue(f *testing.F) {
	for _, seed := range []string{`42`, `"acme/widgets#7"`, `"https://github.example.com/a/b/issues/3"`, `0`, "\xff\xfe", `"\ud800"`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if n, ok := jobs.ParseIssue(json.RawMessage(data)); ok && (n <= 0 || !json.Valid(data)) {
			t.Fatalf("ParseIssue(%q) accepted %d", data, n)
		}
	})
}
