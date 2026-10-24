package fix

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// scriptedAF is a box whose af is a script: it writes the argv of each run to a file and
// answers as its script says. The box is the local provider under the name of one that calls
// its programs by name.
type scriptedAF struct {
	t        *testing.T
	repo     *testRepo
	sess     *box.Session
	m        *BoxMeasurer
	warnings []string
	argv     string // the file the af script logs its arguments to
}

// newScriptedAF makes a box that stands in for a leased one over a new checkout, with a PATH whose
// first directory has the af that body is the shell script of (it may write files in its working
// directory, the root of the box's tree).
func newScriptedAF(t *testing.T, body string) *scriptedAF {
	t.Helper()
	return newScriptedAFIn(t, box.GatewayName, body)
}

// newScriptedAFIn is newScriptedAF with the box of the provider: the gateway, which a local box
// stands in for under its name and calls af by name, or the local box, which runs the af of the
// script by its path.
func newScriptedAFIn(t *testing.T, provider, body string) *scriptedAF {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" >> \"$(dirname \"$0\")/argv.log\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "af"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := newTestRepo(t, gateFiles, false)
	entry, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	reg := box.NewRegistry()
	if provider == box.GatewayName {
		named := entry
		named.Name, named.ByName, named.SuppliesCredentials, named.Remote = box.GatewayName, true, true, true
		named.New = func() sandbox.Provider { return local.Unisolated() }
		reg.Register(named)
	} else {
		reg.Register(entry)
	}
	state := sampleState()
	s := &scriptedAF{t: t, repo: repo}
	sess, err := box.Open(context.Background(), box.Request{Provider: provider, HostDir: repo.Dir, RunID: state.RunID, Tool: "fix", Registry: reg, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	s.sess = sess
	s.m = &BoxMeasurer{
		Session: sess, Process: brain.NewProcess(brain.ProcessConfig{AFPath: filepath.Join(bin, "af"), Version: "v1", Commit: "c1"}), Profile: "py", RunID: state.RunID,
		Warn: func(code, message string) { s.warnings = append(s.warnings, code) },
	}
	s.argv = filepath.Join(bin, "argv.log")
	return s
}

func (s *scriptedAF) runs() []string {
	data, _ := os.ReadFile(s.argv)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && line != "--version" {
			out = append(out, line)
		}
	}
	return out
}

// version is the af --version of the script.
const versionAnswer = `case "$1" in --version) echo '{"version":"v1","commit":"c1"}'; exit 0;; esac`

// envelope is the JSON of an envelope with the result.
func envelope(status, result, extra string) string {
	return `{"schema_version":"5.0.0","tool":"x","status":"` + status + `","exit_code":0,"result":` + result + extra + `}`
}

// TestTheBoxMeasurerGateMirrorsTheResultAndPlacesTheTable verifies what the measurer sends and
// keeps: the command, the table placed in the box, the result and its logs
// copied to the host, and the tree changed during the gate noted from the envelope.
func TestTheBoxMeasurerGateMirrorsTheResultAndPlacesTheTable(t *testing.T) {
	result := `{"id":"abc123def456","tree":"t","table_source":"gate.json","cached":false,"passed":true,"checks":[{"name":"make check","status":"pass","exit_code":0,"duration_ms":5,"log":".agent-fox/gate/abc123def456/01.log","tail":"ok"}],"skipped":[]}`
	s := newScriptedAF(t, versionAnswer+`
mkdir -p .agent-fox/gate/abc123def456
echo 'the log' > .agent-fox/gate/abc123def456/01.log
echo '`+result+`' > .agent-fox/gate/abc123def456/result.json
: > .agent-fox/gate/abc123def456/.tmp-02.log-123
cat .agent-fox/gate.json > .agent-fox/gate/abc123def456/table.json
echo '`+envelope("done", result, `,"warnings":[{"code":"tree_changed_during_gate","message":"m"}]`)+`'`)
	table := project.Table{Source: "gate.json", Checks: []project.Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: "check", Available: true}}}

	res, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{Table: table})

	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "abc123def456" || !res.Passed || !res.TreeChanged || len(res.Checks) != 1 || res.Checks[0].Name != "make check" {
		t.Errorf("result = %+v", res)
	}
	if got := s.runs(); !slices.Equal(got, []string{"gate run"}) {
		t.Errorf("af was run with %q", got)
	}
	kept := filepath.Join(s.repo.Dir, ".agent-fox", "gate", "abc123def456")
	for name, want := range map[string]string{"01.log": "the log\n", "result.json": "", "table.json": ""} {
		data, err := os.ReadFile(filepath.Join(kept, name))
		if err != nil || (want != "" && string(data) != want) {
			t.Errorf("%s on the host: %q, %v", name, data, err)
		}
	}
	if entries, _ := os.ReadDir(kept); slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), ".tmp") }) {
		t.Errorf("a temporary file of the box was copied: %v", entries)
	}
	var placed struct {
		Checks []struct {
			Name string   `json:"name"`
			Argv []string `json:"argv"`
		} `json:"checks"`
	}
	data, _ := os.ReadFile(filepath.Join(kept, "table.json"))
	if err := json.Unmarshal(data, &placed); err != nil || len(placed.Checks) != 1 || placed.Checks[0].Name != "make check" || !slices.Equal(placed.Checks[0].Argv, []string{"make", "check"}) {
		t.Errorf("the table placed in the box: %s (%v)", data, err)
	}
	// A result with no checks and nothing skipped has lists, not nulls.
	bare := newScriptedAF(t, versionAnswer+`
mkdir -p .agent-fox/gate/0123456789ab
echo '{"id":"0123456789ab","tree":"t","passed":false}' > .agent-fox/gate/0123456789ab/result.json
echo '`+envelope("done", `{"id":"0123456789ab","tree":"t","passed":false}`, "")+`'`)
	if res, err := bare.m.Gate(context.Background(), bare.repo.Dir, checks.Options{}); err != nil || res.Checks == nil || res.Skipped == nil || res.TreeChanged {
		t.Errorf("a result with no lists = %+v, %v; want empty lists and the tree unchanged", res, err)
	}
	// The comparison with another result finds the one that was kept.
	if _, _, ok := loadKept(s.repo.Dir, "abc123def456"); !ok {
		t.Error("the comparison cannot find the result on the host")
	}
}

// loadKept reads the result that was kept, as the comparison does.
func loadKept(dir, id string) (checks.Result, string, bool) {
	cmp, err := checks.Compare(dir, id, id)
	return checks.Result{ID: cmp.After.ID}, cmp.Verdict, err == nil && cmp.After.ID == id
}

// TestTheBoxMeasurerDetectAndRevertCheckPassWhatTheyAreAskedAndReadWhatTheyAreAnswered verifies the
// words of the commands and the decoding of their results.
func TestTheBoxMeasurerDetectAndRevertCheckPassWhatTheyAreAskedAndReadWhatTheyAreAnswered(t *testing.T) {
	detection := `{"languages":[{"name":"go","files":3}],"gate":{"source":"detected","checks":[{"name":"make check","argv":["make","check"],"kind":"check","source":"Makefile","available":true}]},"suite":[["make","check"]],"programs":["make"]}`
	reverted := `{"outcome":"proven","reason":"tests_fail_without_the_change","closes":true,"since":"abc","test_files":["t_test.sh"],"reverted_files":["greet.sh"],"after":"x","reverted":null,"restored":true}`
	s := newScriptedAF(t, versionAnswer+`
case "$1" in
  lang) echo '`+envelope("done", detection, "")+`';;
  revert-check) echo '`+envelope("done", reverted, "")+`';;
esac`)

	d, err := s.m.Detect(context.Background(), s.repo.Dir, nil)
	if err != nil || len(d.Gate.Checks) != 1 || d.Gate.Checks[0].Name != "make check" || !d.Gate.Checks[0].Available || d.Gate.Source != "detected" || len(d.Languages) != 1 {
		t.Errorf("Detect = %+v, %v", d, err)
	}
	r, err := s.m.RevertCheck(context.Background(), s.repo.Dir, "abc123", conform.Options{Table: d.Gate})
	if err != nil || r.Outcome != "proven" || !r.Closes || !r.Restored {
		t.Errorf("RevertCheck = %+v, %v", r, err)
	}
	if got := s.runs(); !slices.Equal(got, []string{"lang detect", "revert-check --since abc123"}) {
		t.Errorf("af was run with %q", got)
	}
	if len(s.warnings) != 0 {
		t.Errorf("warnings = %v", s.warnings)
	}
}

// TestTheBoxMeasurerReadsTheFailureOfACommandAndRefusesWhatItCannotUse verifies that a failed
// envelope is the failure it says, and that an answer that is no envelope, has another schema or
// a result that does not decode names the profile and cannot be tried again.
func TestTheBoxMeasurerReadsTheFailureOfACommandAndRefusesWhatItCannotUse(t *testing.T) {
	failed := `{"schema_version":"5.0.0","tool":"lang detect","status":"failed","exit_code":1,"result":null,"error":{"category":"config","message":"gate.json cannot be used","retryable":false}}`
	cases := map[string]struct {
		answer    string
		category  string
		retryable bool
		contains  string
	}{
		"a failure of the command":        {failed, "config", false, "gate.json cannot be used"},
		"a failure that may be retried":   {strings.Replace(failed, `"retryable":false`, `"retryable":true`, 1), "config", true, "gate.json cannot be used"},
		"a failed envelope with no error": {`{"schema_version":"5.0.0","status":"failed","result":null}`, "sandbox", false, "no error"},
		"another major version":           {`{"schema_version":"6.0.0","status":"done","result":{}}`, "sandbox", false, "schema 6.0.0"},
		"a schema version that is none":   {`{"schema_version":"x","status":"done","result":{}}`, "sandbox", false, "schema x"},
		"no envelope":                     {`nothing`, "sandbox", false, "no envelope"},
		"a null result":                   {`{"schema_version":"5.0.0","status":"done","result":null}`, "sandbox", false, "no result"},
		"a result of another shape":       {`{"schema_version":"5.0.0","status":"done","result":{"gate":7}}`, "sandbox", false, "does not decode"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newScriptedAF(t, versionAnswer+"\necho '"+tc.answer+"'")
			_, err := s.m.Detect(context.Background(), s.repo.Dir, nil)
			var f *toolio.Failure
			if !errors.As(err, &f) || f.Category != tc.category || f.Retryable != tc.retryable || !strings.Contains(f.Message, tc.contains) {
				t.Fatalf("Detect = %v, want category %s, retryable %v, containing %q", err, tc.category, tc.retryable, tc.contains)
			}
			if tc.category == "sandbox" && (!strings.Contains(f.Message, "the profile py") || f.FixHint == nil || f.FixHint.Env != "AF_SANDBOX_PROFILE") {
				t.Errorf("the failure %+v does not name the profile and where to set it", f)
			}
		})
	}

	t.Run("a default profile is named as hub's", func(t *testing.T) {
		s := newScriptedAF(t, versionAnswer+"\necho nothing")
		s.m.Profile = ""
		_, err := s.m.Detect(context.Background(), s.repo.Dir, nil)
		if err == nil || !strings.Contains(err.Error(), "hub's default profile") {
			t.Errorf("Detect = %v, want the default profile named", err)
		}
	})
	t.Run("an af that is not there", func(t *testing.T) {
		s := newScriptedAF(t, "exit 3")
		_, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{})
		var f *toolio.Failure
		if !errors.As(err, &f) || f.Category != "sandbox" || f.Retryable || !strings.Contains(f.Message, "its image must provide af") || !strings.Contains(f.Message, "the profile py") {
			t.Errorf("Gate = %v, want a permanent failure of the image of the profile", err)
		}
		if len(s.runs()) != 0 {
			t.Errorf("a ring-1 command ran: %v", s.runs())
		}
	})
	t.Run("an af that is not there in a local box", func(t *testing.T) {
		s := newScriptedAFIn(t, "local", "exit 3")
		_, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{})
		var f *toolio.Failure
		if !errors.As(err, &f) || f.Category != "sandbox" || f.Retryable || !strings.Contains(f.Message, "the local box") || strings.Contains(f.Message, "profile") || f.FixHint != nil {
			t.Errorf("Gate = %v (%+v), want a permanent failure that names the local box, and no profile", err, f)
		}
	})
	t.Run("a gate result with no id", func(t *testing.T) {
		s := newScriptedAF(t, versionAnswer+"\necho '"+envelope("done", `{"id":"../x","passed":true,"checks":[]}`, "")+"'")
		if _, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{}); err == nil || !strings.Contains(err.Error(), `"../x"`) {
			t.Errorf("Gate = %v, want the id refused", err)
		}
	})
}
