package toolio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// envelopeKeys are the envelope's fields, in order.
var envelopeKeys = []string{
	"schema_version", "tool", "run_id", "status", "exit_code", "summary", "input", "result",
	"needs_human", "error", "warnings", "artifacts", "side_effects", "next", "usage",
	"started_at", "finished_at", "duration_ms",
}

// topLevelKeys returns the keys of the JSON object in raw, in document order.
func topLevelKeys(t *testing.T, raw string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("stdout does not start with an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading key: %v", err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("reading value of %v: %v", tok, err)
		}
	}
	return keys
}

// TestStdoutIsOneIndentedObject_TS_05_9 verifies TS-05-9:
// Stdout holds exactly one two-space-indented JSON object ending in one
// newline.
// Verifies: 05-REQ-2.1
func TestStdoutIsOneIndentedObject_TS_05_9(t *testing.T) {
	app := newTestApp()
	app.Register(returning("X", Outcome{Status: StatusDone, Summary: "ok"}, nil))

	_, stdout, stderr := invoke(t, app, "X")

	dec := json.NewDecoder(strings.NewReader(stdout))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, stdout)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Errorf("stdout holds data after the object (err = %v)", err)
	}

	var compact, indented bytes.Buffer
	if err := json.Compact(&compact, []byte(stdout)); err != nil {
		t.Fatal(err)
	}
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	if want := indented.String() + "\n"; stdout != want {
		t.Errorf("stdout is not two-space indented with one trailing newline:\n%q\nwant\n%q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// TestEnvelopeFieldOrder_TS_05_10 verifies TS-05-10:
// Envelope fields appear in the fixed order with every field present.
// Verifies: 05-REQ-2.2
func TestEnvelopeFieldOrder_TS_05_10(t *testing.T) {
	hint := &FixHint{Flag: "--timeout", Env: "GITHUB_TOKEN", Var: "v", Suggest: "s"}
	endings := []struct {
		name string
		out  Outcome
		err  error
	}{
		{"done", Outcome{Status: StatusDone, Summary: "ok"}, nil},
		{"failed", Outcome{}, &Failure{Category: CategoryForge, Message: "m", FixHint: hint}},
		{"usage", Outcome{}, &Failure{Category: CategoryUsage, Message: "bad"}},
		{"needs_human", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: []string{"x", "y"}}}, nil},
		{"unverified", Outcome{Status: StatusUnverified, Summary: "unchecked"}, nil},
	}
	allowedHint := []string{"flag", "env", "var", "suggest"}

	for _, tc := range endings {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp()
			app.Register(returning("X", tc.out, tc.err))

			_, stdout, _ := invoke(t, app, "X")

			if got := topLevelKeys(t, stdout); !slices.Equal(got, envelopeKeys) {
				t.Fatalf("keys = %v, want %v", got, envelopeKeys)
			}
			env := decode(t, stdout)
			if env["schema_version"] != "4.0.0" {
				t.Errorf("schema_version = %v, want 4.0.0", env["schema_version"])
			}
			for _, key := range []string{"warnings", "artifacts", "side_effects", "next"} {
				if list, ok := env[key].([]any); !ok || len(list) != 0 {
					t.Errorf("%s = %v, want []", key, env[key])
				}
			}
			if h, ok := at(env, "error", "fix_hint").(map[string]any); ok {
				for k := range h {
					if !slices.Contains(allowedHint, k) {
						t.Errorf("fix_hint has the key %q, want only %v", k, allowedHint)
					}
				}
			}
			if tc.name == "usage" && at(env, "error", "fix_hint") != nil {
				t.Errorf("fix_hint = %v, want null", at(env, "error", "fix_hint"))
			}
			if tc.name == "needs_human" {
				if opts, ok := at(env, "needs_human", "options").([]any); !ok || len(opts) != 2 {
					t.Errorf("needs_human.options = %v, want two options", at(env, "needs_human", "options"))
				}
			}
		})
	}
}

// TestStatusExitCodeAndNullRules_TS_05_11 verifies TS-05-11:
// Status, exit code and the null-iff rules hold for every possible ending.
// Verifies: 05-REQ-2.3
func TestStatusExitCodeAndNullRules_TS_05_11(t *testing.T) {
	codes := map[string]int{"done": 0, "failed": 1, "usage": 2, "needs_human": 3, "unverified": 4}
	categories := []string{CategoryUsage, CategoryAuth, CategoryForge, CategoryIO, CategoryCancelled, CategoryTimeout, CategoryInternal, "budget"}
	rng := rand.New(rand.NewPCG(5, 11))

	for i := range 300 {
		var (
			name string
			end  func() (Outcome, error)
		)
		switch rng.IntN(6) {
		case 0:
			name = "done"
			end = func() (Outcome, error) { return Outcome{Status: StatusDone}, nil }
		case 1:
			name = "unverified"
			end = func() (Outcome, error) { return Outcome{Status: StatusUnverified}, nil }
		case 2:
			options := make([]string, rng.IntN(27))
			for j := range options {
				options[j] = fmt.Sprintf("option %d", j)
			}
			name = fmt.Sprintf("needs_human with %d options", len(options))
			end = func() (Outcome, error) {
				return Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: options}}, nil
			}
		case 3:
			f := &Failure{Category: categories[rng.IntN(len(categories))], Message: "m", Retryable: rng.IntN(2) == 0}
			name = "failure " + f.Category
			end = func() (Outcome, error) { return Outcome{}, f }
		case 4:
			name = "plain error"
			end = func() (Outcome, error) { return Outcome{}, errors.New("plain") }
		default:
			name = "panic"
			end = func() (Outcome, error) { panic("boom") }
		}

		app := newTestApp()
		app.Register(Command{Path: "X", Run: func(*Run) (Outcome, error) { return end() }})
		code, stdout, _ := invoke(t, app, "X")
		env := decode(t, stdout)

		status, _ := env["status"].(string)
		want, known := codes[status]
		if !known {
			t.Fatalf("#%d %s: status %q is not one of the five", i, name, status)
		}
		if code != want || env["exit_code"] != float64(want) {
			t.Errorf("#%d %s: Main returned %d and exit_code is %v, want %d for %s", i, name, code, env["exit_code"], want, status)
		}
		if (env["needs_human"] != nil) != (status == "needs_human") {
			t.Errorf("#%d %s: needs_human = %v with status %s", i, name, env["needs_human"], status)
		}
		if (env["error"] != nil) != (status == "failed" || status == "usage") {
			t.Errorf("#%d %s: error = %v with status %s", i, name, env["error"], status)
		}
	}
}

// TestUnencodableResultStaysOneEnvelope guards the rule behind TS-05-9: a
// result that cannot be encoded as JSON still leaves one envelope on stdout.
func TestUnencodableResultStaysOneEnvelope(t *testing.T) {
	app := newTestApp()
	app.Register(returning("X", Outcome{Status: StatusDone, Result: make(chan int)}, nil))

	code, stdout, _ := invoke(t, app, "X")
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryInternal {
		t.Errorf("got exit code %d, status %v, category %v; want 1, failed, internal", code, env["status"], at(env, "error", "category"))
	}
}
