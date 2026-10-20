package brain

import (
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// randomString is a string that may hold multi-byte characters, newlines and
// the empty string.
func randomString(rng *rand.Rand) string {
	alphabet := []rune("abcXYZ019 \n\t\r\"'\\é☃日本語\x00\u2028🙂")
	var b strings.Builder
	for range rng.IntN(40) {
		b.WriteRune(alphabet[rng.IntN(len(alphabet))])
	}
	return b.String()
}

// TestDocumentIsPrefixThenTask_TS_07_1 verifies TS-07-1:
// Document() is always Prefix followed by Task, byte for byte.
// Verifies: 07-REQ-1.1
func TestDocumentIsPrefixThenTask_TS_07_1(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 1))
	cases := [][2]string{{"", ""}, {"p", ""}, {"", "t"}, {"line\n", "\nline"}, {"é", "☃"}}
	for range 500 {
		cases = append(cases, [2]string{randomString(rng), randomString(rng)})
	}
	for _, c := range cases {
		prefix, task := c[0], c[1]
		got := Brief{Prefix: prefix, Task: task}.Document()
		if got != prefix+task || !strings.HasPrefix(got, prefix) || len(got) != len(prefix)+len(task) {
			t.Fatalf("Document(%q, %q) = %q", prefix, task, got)
		}
		if utf8.ValidString(prefix) && utf8.ValidString(task) && !utf8.ValidString(got) {
			t.Fatalf("Document(%q, %q) is not valid UTF-8", prefix, task)
		}
	}
}

// parseFlags registers the shared flags on a flag set with the defaults and
// parses the arguments.
func parseFlags(t *testing.T, args ...string) (*Settings, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(discard{})
	s := Flags(fs, Defaults{Brain: "agentkit", Sandbox: "local", Budget: 5, MaxTurns: 40})
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return s, nil
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// usageNaming says that err is a usage failure that names the flag.
func usageNaming(t *testing.T, err error, name string) {
	t.Helper()
	f, ok := err.(*toolio.Failure)
	if !ok || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, name) {
		t.Errorf("error = %v (%T), want a usage failure that names %s", err, err, name)
	}
}

// TestBudgetAndTurnsAreChecked_TS_07_3 verifies TS-07-3:
// a non-finite or non-positive budget and a max-turns below 1 are usage errors
// naming the flag.
// Verifies: 07-REQ-1.3
func TestBudgetAndTurnsAreChecked_TS_07_3(t *testing.T) {
	for _, v := range []string{"0", "-1", "-0.01", "NaN", "+Inf", "-Inf", "Inf"} {
		s, err := parseFlags(t, "--budget", v)
		if err != nil {
			t.Fatalf("--budget %s: the flag package refused it: %v", v, err)
		}
		usageNaming(t, s.Validate(), "--budget")
	}
	for _, v := range []string{"0", "-3"} {
		s, err := parseFlags(t, "--max-turns", v)
		if err != nil {
			t.Fatalf("--max-turns %s: the flag package refused it: %v", v, err)
		}
		usageNaming(t, s.Validate(), "--max-turns")
	}
	// What is not a number never gets as far as Validate: the flag package says
	// which flag it was.
	if _, err := parseFlags(t, "--budget", "lots"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("--budget lots: %v, want an error that names the flag", err)
	}
	if _, err := parseFlags(t, "--max-turns", "1.5"); err == nil || !strings.Contains(err.Error(), "max-turns") {
		t.Errorf("--max-turns 1.5: %v, want an error that names the flag", err)
	}
	for _, args := range [][]string{nil, {"--budget", "0.01"}, {"--budget", "1e3", "--max-turns", "1"}} {
		s, err := parseFlags(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(); err != nil {
			t.Errorf("%v: Validate = %v, want nil", args, err)
		}
	}
}

// TestScriptGoesWithScripted_TS_07_4 verifies TS-07-4:
// --script without scripted, and scripted without --script, are usage errors
// naming --script.
// Verifies: 07-REQ-1.4
func TestScriptGoesWithScripted_TS_07_4(t *testing.T) {
	for _, args := range [][]string{
		{"--brain", "scripted"},
		{"--brain", "agentkit", "--script", "x.json"},
		{"--script", "x.json"}, // the default brain is agentkit
		{"--brain", "other", "--script", "x.json"},
	} {
		s, err := parseFlags(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		usageNaming(t, s.Validate(), "--script")
	}
	s, err := parseFlags(t, "--brain", "scripted", "--script", "x.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil || s.Script != "x.json" || s.Brain != "scripted" {
		t.Errorf("scripted with a script: %+v, %v", s, err)
	}
}

// TestSandboxDefaultsToLocal_TS_07_6 verifies TS-07-6:
// without --sandbox the shared flags select local.
// Verifies: 07-REQ-1.6
func TestSandboxDefaultsToLocal_TS_07_6(t *testing.T) {
	for _, d := range []Defaults{{Sandbox: "local"}, {}} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		s := Flags(fs, d)
		if err := fs.Parse(nil); err != nil {
			t.Fatal(err)
		}
		if s.Sandbox != "local" {
			t.Errorf("defaults %+v: sandbox = %q, want local", d, s.Sandbox)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	s := Flags(fs, Defaults{Sandbox: "local", Brain: "scripted", Budget: 2.5, MaxTurns: 9})
	if err := fs.Parse([]string{"--sandbox", "podman"}); err != nil || s.Sandbox != "podman" || s.Brain != "scripted" || s.Budget != 2.5 || s.MaxTurns != 9 {
		t.Errorf("settings = %+v, %v; want the command's defaults, and the sandbox that was asked for", s, err)
	}
}

// TestBoxEnvIsTheHostValuesOfBoxEnv_TS_07_8 verifies TS-07-8:
// the box environment equals exactly the host values of BoxEnv names.
// Verifies: 07-REQ-1.8
func TestBoxEnvIsTheHostValuesOfBoxEnv_TS_07_8(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	pool := []string{"A", "B", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "GITLAB_TOKEN", "PATH", "HOME", "X_1", "EMPTY", "ünï"}
	for i := range 500 {
		var names []string
		for range rng.IntN(5) {
			names = append(names, pool[rng.IntN(len(pool))])
		}
		host := map[string]string{}
		for _, n := range pool {
			if rng.IntN(2) == 0 {
				host[n] = randomString(rng)
			}
		}
		host["UNRELATED_"+fmt.Sprint(i)] = "x"
		want := map[string]string{}
		for _, n := range names {
			if v, ok := host[n]; ok {
				want[n] = v
			}
		}

		got := BuildBoxEnv(Entry{BoxEnv: names}, func(name string) (string, bool) { v, ok := host[name]; return v, ok })

		if len(got) != len(want) {
			t.Fatalf("#%d: names %v, host %v: env = %v, want %v", i, names, host, got, want)
		}
		for k, v := range want {
			if g, ok := got[k]; !ok || g != v {
				t.Fatalf("#%d: env[%q] = %q (%v), want %q", i, k, g, ok, v)
			}
		}
	}
	if env := BuildBoxEnv(Entry{}, func(string) (string, bool) { return "x", true }); env == nil || len(env) != 0 {
		t.Errorf("an entry with no BoxEnv gets %#v, want an empty map", env)
	}
}

// TestRegistry guards 07-REQ-1.2: brains are found by name, and a name that is
// not registered is a usage failure that lists the registered ones and
// constructs nothing.
func TestRegistry(t *testing.T) {
	r := NewRegistry()
	var built []string
	for _, name := range []string{"zeta", "alpha"} {
		r.Register(Entry{Name: name, New: func(Options) (Brain, error) { built = append(built, name); return nil, nil }})
	}

	if names := r.Names(); !slices.Equal(names, []string{"alpha", "zeta"}) {
		t.Errorf("Names = %v, want them sorted", names)
	}
	if _, err := r.New("alpha", Options{}); err != nil || !slices.Equal(built, []string{"alpha"}) {
		t.Errorf("New(alpha) = %v, built %v", err, built)
	}
	_, err := r.New("claude-x", Options{})
	f, ok := err.(*toolio.Failure)
	if !ok || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "alpha") || !strings.Contains(f.Message, "zeta") || !strings.Contains(f.Message, "claude-x") {
		t.Errorf("error = %v, want a usage failure that names claude-x and lists the registered brains", err)
	}
	if !slices.Equal(built, []string{"alpha"}) {
		t.Errorf("a brain was built for an unknown name: %v", built)
	}
	r.Register(Entry{Name: "alpha", CostSource: CostNone})
	if e, err := r.Lookup("alpha"); err != nil || e.CostSource != CostNone {
		t.Errorf("a registration does not replace: %+v, %v", e, err)
	}
}

// TestTally guards 07-REQ-6.7: outcomes add up, in cost, turns and tokens, and
// the source of the cost is the best that any attempt had.
func TestTally(t *testing.T) {
	var tally Tally
	if u := tally.Usage("scripted"); u != (toolio.Usage{Brain: "scripted", CostSource: CostNone}) {
		t.Errorf("an empty tally = %+v", u)
	}

	tally.Add(Outcome{CostUSD: 0.10, Turns: 3, InputTokens: 100, OutputTokens: 50, CostSource: CostReported})
	tally.Add(Outcome{CostUSD: 0.20, Turns: 2, InputTokens: 10, OutputTokens: 5, CostSource: CostMeasured})
	tally.Add(Outcome{}) // an attempt that said nothing

	want := toolio.Usage{Brain: "agentkit", CostUSD: 0.30000000000000004, CostSource: CostMeasured, InputTokens: 110, OutputTokens: 55, Turns: 5}
	if got := tally.Usage("agentkit"); got != want {
		t.Errorf("Usage = %+v, want %+v", got, want)
	}
	if math.Abs(tally.Usage("x").CostUSD-0.30) > 1e-9 {
		t.Errorf("cost = %v", tally.Usage("x").CostUSD)
	}
}
