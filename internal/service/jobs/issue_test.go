package jobs

import (
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

var (
	oracleInteger = regexp.MustCompile(`^[1-9][0-9]*$`)
	oraclePath    = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+#([0-9]+)$`)
)

// oracleIssue is the rule of 12-REQ-3.3 written out again, for any value: a JSON
// positive integer; a string of at most 2048 bytes that issuex.ParseIssueURL accepts
// and does not mark as a pull or merge request; or <path>#<N>.
func oracleIssue(raw string) (int64, bool) {
	if oracleInteger.MatchString(raw) {
		n, err := strconv.ParseInt(raw, 10, 64)
		return n, err == nil
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil || len(s) > 2048 {
		return 0, false
	}
	if ref, ok := issuex.ParseIssueURL(s); ok {
		return int64(ref.Number), !ref.IsPullRequest
	}
	if m := oraclePath.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseInt(m[2], 10, 64)
		return n, err == nil && n > 0
	}
	return 0, false
}

// issueValues are values of every class the rule distinguishes.
func issueValues(rng *rand.Rand) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	long := strings.Repeat("a", 2100)
	values := []string{
		// numbers
		"1", "42", "9007199254740993", "9223372036854775807", "9223372036854775808", "99999999999999999999",
		"0", "-1", "-0", "4.2", "42.0", "4e1", "1E2", "01", "+1", "1 ", " 1", "null", "true", "false", "[]", "{}", `[42]`, `{"n":42}`,
		// issue URLs of GitHub and GitLab
		q("https://github.example.com/acme/widgets/issues/42"),
		q("https://github.example.com/acme/widgets/issues/42#issuecomment-1"),
		q("http://github.example.com/acme/widgets/issues/7"),
		q("https://gitlab.example.com/acme/widgets/-/issues/42"),
		q("https://gitlab.example.com/group/sub/widgets/-/issues/9"),
		// pull and merge requests
		q("https://github.example.com/acme/widgets/pull/3"),
		q("https://github.example.com/acme/widgets/pull/3/files"),
		q("https://gitlab.example.com/acme/widgets/-/merge_requests/5"),
		// other URLs and strings
		q("https://example.com/acme/widgets/issues/42"),
		q("https://github.example.com/acme/widgets"),
		q("https://github.example.com/acme/widgets/issues/zero"),
		q("ftp://github.example.com/acme/widgets/issues/42"),
		q("42"), q(""), q("#42"), q("widgets#42"), q("acme/widgets"), q("acme/widgets#"), q("acme/widgets#0"), q("acme/widgets#-3"),
		q("acme/widgets#4x"), q("acme/widgets#42 "), q(" acme/widgets#42"), q("acme//widgets#42"), q("/acme/widgets#42"), q("acme/widgets/#42"),
		q("acme/wid gets#42"), q("acme/wid$gets#42"), q("acme/widgets#99999999999999999999"),
		// owner/repo#N
		q("acme/widgets#42"), q("a/b#1"), q("group/sub/widgets#7"), q("a.b/c_d-e#1234"), q("a/b/c/d/e#8"), q("./.#3"),
		// over the length
		q(long), q(long + "/x#1"), q(strings.Repeat("a/", 1100) + "x#5"),
		// at the length, and a byte over it
		q("a/b" + strings.Repeat("a", 2042) + "#99"), q("a/b" + strings.Repeat("a", 2043) + "#99"),
	}
	// generated: <path>#<N> with segments of the right and the wrong characters
	alphabet := []rune("abcXYZ019_.-")
	bad := []rune(" $@!:#?")
	for range 200 {
		var path []string
		for range 1 + rng.IntN(4) {
			seg := make([]rune, 1+rng.IntN(5))
			for i := range seg {
				if rng.IntN(12) == 0 {
					seg[i] = bad[rng.IntN(len(bad))]
				} else {
					seg[i] = alphabet[rng.IntN(len(alphabet))]
				}
			}
			path = append(path, string(seg))
		}
		n := []string{"1", "17", "0", "007", "x", "", "123456789012"}[rng.IntN(7)]
		values = append(values, q(strings.Join(path, "/")+"#"+n))
	}
	return values
}

// TestAnIssueIsAcceptedExactlyAsTheRuleSays_TS_12_17 verifies TS-12-17: an issue
// value is accepted for a positive integer, for an issue URL that is not a pull or
// merge request, and for <path>#<N>, and for nothing else; the number is the one
// the value holds.
// Verifies: 12-REQ-3.3
func TestAnIssueIsAcceptedExactlyAsTheRuleSays_TS_12_17(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 17))
	accepted, refused := 0, 0
	for _, raw := range issueValues(rng) {
		wantN, want := oracleIssue(raw)
		gotN, got := ParseIssue(json.RawMessage(raw))
		if got != want || (got && gotN != wantN) {
			t.Errorf("ParseIssue(%.80s) = %d, %v, want %d, %v", raw, gotN, got, wantN, want)
		}
		if got {
			accepted++
		} else {
			refused++
		}
	}
	if accepted < 20 || refused < 40 {
		t.Errorf("the values hold %d accepted and %d refused, want both classes well represented", accepted, refused)
	}

	// The cases the rule is about, by name.
	for raw, want := range map[string]int64{
		`42`: 42, `"https://github.example.com/acme/widgets/issues/42"`: 42, `"https://gitlab.example.com/g/s/w/-/issues/9"`: 9, `"acme/widgets#7"`: 7,
	} {
		if n, ok := ParseIssue(json.RawMessage(raw)); !ok || n != want {
			t.Errorf("ParseIssue(%s) = %d, %v, want %d", raw, n, ok, want)
		}
	}
	for _, raw := range []string{
		`0`, `-5`, `4.5`, `1e3`, `"https://github.example.com/acme/widgets/pull/3"`, `"https://gitlab.example.com/acme/widgets/-/merge_requests/5"`,
		`"https://example.com/acme/widgets/issues/42"`, `"widgets#42"`, `"42"`, ``, `null`, `"acme/widgets#0"`,
	} {
		if n, ok := ParseIssue(json.RawMessage(raw)); ok {
			t.Errorf("ParseIssue(%s) = %d, true, want it refused", raw, n)
		}
	}
}

// FuzzParseIssue holds the invariants of ParseIssue for any bytes: it does not
// panic, a number it accepts is positive, and it agrees with the rule.
func FuzzParseIssue(f *testing.F) {
	for _, raw := range issueValues(rand.New(rand.NewPCG(12, 170))) {
		f.Add([]byte(raw))
	}
	f.Add([]byte("\xff\xfe"))
	f.Add([]byte(`"\ud800"`))
	f.Fuzz(func(t *testing.T, data []byte) {
		n, ok := ParseIssue(json.RawMessage(data))
		if ok && n <= 0 {
			t.Fatalf("ParseIssue(%q) accepted the number %d", data, n)
		}
		if !json.Valid(data) {
			if ok {
				t.Fatalf("ParseIssue(%q) accepted bytes that are not JSON", data)
			}
			return
		}
		if wantN, want := oracleIssue(string(data)); ok != want || (ok && n != wantN) {
			t.Fatalf("ParseIssue(%q) = %d, %v, want %d, %v", data, n, ok, wantN, want)
		}
	})
}
