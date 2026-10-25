package fix

import (
	"context"
	"maps"
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
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// scriptedAF is a box whose af is a shell script that logs the arguments of each run and
// answers as its body says, in the working directory of the box.
type scriptedAF struct {
	repo     *repo
	m        *BoxMeasurer
	warnings []string
	argv     string
}

// versionAnswer is the af --version of a script, the version of the af that runs.
const versionAnswer = `case "$1" in --version) echo '{"version":"v1","commit":"c1"}'; exit 0;; esac`

// newScriptedAF makes the box of the provider over a new checkout: the gateway, which a local
// box stands in for under its name and which calls af by name, or a local box, which runs af
// by its path.
func newScriptedAF(t *testing.T, provider, body string) *scriptedAF {
	t.Helper()
	testutil.Require(t, "sh")
	bin := t.TempDir()
	script := testutil.WriteFile(t, bin, "af", "#!/bin/sh\necho \"$@\" >> \"$(dirname \"$0\")/argv.log\"\n"+body+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := newRepo(t, gateFiles(), false)
	entry, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	reg := box.NewRegistry()
	if provider == box.GatewayName {
		entry.Name, entry.ByName, entry.SuppliesCredentials, entry.Remote = box.GatewayName, true, true, true
		entry.New = func() sandbox.Provider { return local.Unisolated() }
	}
	reg.Register(entry)
	sess, err := box.Open(context.Background(), box.Request{Provider: provider, HostDir: r.Dir, RunID: runID, Tool: "fix", Registry: reg, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	s := &scriptedAF{repo: r, argv: filepath.Join(bin, "argv.log")}
	s.m = &BoxMeasurer{
		Session: sess, Process: brain.NewProcess(brain.ProcessConfig{AFPath: script, Version: "v1", Commit: "c1"}), Profile: "py", RunID: runID,
		Warn: func(code, _ string) { s.warnings = append(s.warnings, code) },
	}
	return s
}

// runs are the commands af was run with, its --version left out.
func (s *scriptedAF) runs() []string {
	data, _ := os.ReadFile(s.argv)
	return slices.DeleteFunc(strings.Split(strings.TrimSpace(string(data)), "\n"), func(l string) bool { return l == "" || l == "--version" })
}

// envelope is the JSON of an envelope with the result.
func envelope(status, result, extra string) string {
	return `{"schema_version":"5.0.0","tool":"x","status":"` + status + `","exit_code":0,"result":` + result + extra + `}`
}

// The table of the run is placed in the box, and the result of the gate and its logs are kept
// on the host, where the comparison with the baseline reads them.
func TestBoxMeasurerGate_PlacesTheTableAndKeepsTheResultOnTheHost(t *testing.T) {
	result := `{"id":"abc123def456","tree":"t","table_source":"gate.json","passed":true,"checks":[{"name":"make check","status":"pass","exit_code":0,"duration_ms":5,"log":".agent-fox/gate/abc123def456/01.log","tail":"ok"}],"skipped":[]}`
	s := newScriptedAF(t, box.GatewayName, versionAnswer+`
mkdir -p .agent-fox/gate/abc123def456
echo 'the log' > .agent-fox/gate/abc123def456/01.log
echo '`+result+`' > .agent-fox/gate/abc123def456/result.json
: > .agent-fox/gate/abc123def456/.tmp-02.log-123
cat .agent-fox/gate.json > .agent-fox/gate/abc123def456/table.json
echo '`+envelope("done", result, `,"warnings":[{"code":"tree_changed_during_gate","message":"m"}]`)+`'`)
	table := project.Table{Source: "gate.json", Checks: []project.Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: "check", Available: true}}}

	res, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{Table: table})

	if err != nil || res.ID != "abc123def456" || !res.Passed || !res.TreeChanged || len(res.Checks) != 1 || res.Checks[0].Name != "make check" {
		t.Fatalf("Gate = %+v, %v", res, err)
	}
	if got := s.runs(); !slices.Equal(got, []string{"gate run"}) {
		t.Errorf("af was run with %q", got)
	}
	kept := testutil.Files(t, filepath.Join(s.repo.Dir, ".agent-fox", "gate", "abc123def456"))
	if kept["01.log"] != "the log\n" || kept["result.json"] == "" || len(kept) != 3 {
		t.Errorf("the host keeps %v, want the log, the result and the table, and no temporary file", slices.Sorted(maps.Keys(kept)))
	}
	placed := testutil.Decode[struct {
		Checks []struct {
			Name string   `json:"name"`
			Argv []string `json:"argv"`
		} `json:"checks"`
	}](t, []byte(kept["table.json"]))
	if len(placed.Checks) != 1 || placed.Checks[0].Name != "make check" || !slices.Equal(placed.Checks[0].Argv, []string{"make", "check"}) {
		t.Errorf("the table placed in the box: %s", kept["table.json"])
	}
	if cmp, err := checks.Compare(s.repo.Dir, res.ID, res.ID); err != nil || cmp.After.ID != res.ID {
		t.Errorf("the comparison cannot find the result on the host: %v", err)
	}

	bare := newScriptedAF(t, box.GatewayName, versionAnswer+`
mkdir -p .agent-fox/gate/0123456789ab
echo '`+envelope("done", `{"id":"0123456789ab","tree":"t","passed":false}`, "")+`'`)
	if res, err := bare.m.Gate(context.Background(), bare.repo.Dir, checks.Options{}); err != nil || res.Checks == nil || res.Skipped == nil || res.TreeChanged {
		t.Errorf("a result with no lists = %+v, %v, want empty lists and the tree unchanged", res, err)
	}
}

func TestBoxMeasurer_DetectsAndChecksTheRevertWithTheAfOfTheBox(t *testing.T) {
	detection := `{"languages":[{"name":"go","files":3}],"gate":{"source":"detected","checks":[{"name":"make check","argv":["make","check"],"kind":"check","source":"Makefile","available":true}]},"suite":[["make","check"]],"programs":["make"]}`
	reverted := `{"outcome":"proven","reason":"tests_fail_without_the_change","closes":true,"since":"abc","restored":true}`
	s := newScriptedAF(t, box.GatewayName, versionAnswer+`
case "$1" in
  lang) [ -f .agent-fox/gate.json ] && grep -q own .agent-fox/gate.json && echo '`+envelope("done", detection, "")+`';;
  revert-check) echo '`+envelope("done", reverted, "")+`';;
esac`)
	testutil.WriteFile(t, s.repo.Dir, ".agent-fox/gate.json", `{"checks":[{"name":"own","argv":["true"]}]}`)

	d, err := s.m.Detect(context.Background(), s.repo.Dir, nil)
	if err != nil || len(d.Gate.Checks) != 1 || d.Gate.Checks[0].Name != "make check" || d.Gate.Source != "detected" || len(d.Languages) != 1 {
		t.Errorf("Detect = %+v, %v, want the detection of the box, made with the checkout's own gate.json", d, err)
	}
	r, err := s.m.RevertCheck(context.Background(), s.repo.Dir, "abc123", conform.Options{Table: d.Gate})
	if err != nil || r.Outcome != conform.OutcomeProven || !r.Closes || !r.Restored {
		t.Errorf("RevertCheck = %+v, %v", r, err)
	}
	if got := s.runs(); !slices.Equal(got, []string{"lang detect", "revert-check --since abc123"}) || len(s.warnings) != 0 {
		t.Errorf("af was run with %q, warnings %v", got, s.warnings)
	}

	testutil.WriteFile(t, s.repo.Dir, ".agent-fox/gate.json", "\xff\xfe")
	if _, err := s.m.Detect(context.Background(), s.repo.Dir, nil); failure(t, err).Category != toolio.CategoryConfig {
		t.Errorf("a gate.json that is not text: %v, want config", err)
	}
}

// A failed envelope is the failure it says; an answer that is no envelope, of another schema,
// or with a result that does not decode, is an af this run cannot use, which names where to
// get another and cannot be tried again.
func TestBoxMeasurer_ReadsAFailureAndRefusesAnAnswerItCannotUse(t *testing.T) {
	failed := `{"schema_version":"5.0.0","tool":"lang detect","status":"failed","exit_code":1,"result":null,"error":{"category":"config","message":"gate.json cannot be used","retryable":false}}`
	for name, tc := range map[string]struct {
		answer, category string
		retryable        bool
		says             string
	}{
		"a failure of the command":        {failed, "config", false, "gate.json cannot be used"},
		"a failure that may be retried":   {strings.Replace(failed, `"retryable":false`, `"retryable":true`, 1), "config", true, "gate.json cannot be used"},
		"a failed envelope with no error": {`{"schema_version":"5.0.0","status":"failed","result":null}`, "sandbox", false, "no error"},
		"another major version":           {`{"schema_version":"6.0.0","status":"done","result":{}}`, "sandbox", false, "schema 6.0.0"},
		"a schema version that is none":   {`{"schema_version":"x","status":"done","result":{}}`, "sandbox", false, "schema x"},
		"no envelope":                     {`nothing`, "sandbox", false, "no envelope"},
		"a null result":                   {`{"schema_version":"5.0.0","status":"done","result":null}`, "sandbox", false, "no result"},
		"a result of another shape":       {`{"schema_version":"5.0.0","status":"done","result":{"gate":7}}`, "sandbox", false, "does not decode"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newScriptedAF(t, box.GatewayName, versionAnswer+"\necho '"+tc.answer+"'")

			_, err := s.m.Detect(context.Background(), s.repo.Dir, nil)

			f := failure(t, err)
			if f.Category != tc.category || f.Retryable != tc.retryable || !strings.Contains(f.Message, tc.says) {
				t.Errorf("Detect = %+v, want %s, retryable %v, saying %q", f, tc.category, tc.retryable, tc.says)
			}
			if tc.category == "sandbox" && (!strings.Contains(f.Message, "the profile py") || f.FixHint == nil || f.FixHint.Env != "AF_SANDBOX_PROFILE") {
				t.Errorf("the failure %+v does not name the profile and where to set it", f)
			}
		})
	}

	s := newScriptedAF(t, box.GatewayName, versionAnswer+"\necho '"+envelope("done", `{"id":"../x","passed":true,"checks":[]}`, "")+"'")
	s.m.Profile = ""
	if _, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{}); err == nil || !strings.Contains(err.Error(), `"../x"`) || !strings.Contains(err.Error(), "hub's default profile") {
		t.Errorf("Gate = %v, want the id of the result refused, naming hub's default profile", err)
	}
}

// An af that is missing or fails in the box is the failure of its image, which cannot be tried
// again; the box is named by its profile, or by its provider when it has none.
func TestBoxMeasurer_AnAfThatCannotRunNamesTheImageThatHasToProvideIt(t *testing.T) {
	for _, tc := range []struct {
		provider, names, notNamed, env string
	}{{box.GatewayName, "the profile py", "", "AF_SANDBOX_PROFILE"}, {"local", "the local box", "profile", ""}} {
		s := newScriptedAF(t, tc.provider, "exit 3")

		_, err := s.m.Gate(context.Background(), s.repo.Dir, checks.Options{})

		f := failure(t, err)
		if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, "its image must provide af") || !strings.Contains(f.Message, tc.names) ||
			(tc.notNamed != "" && strings.Contains(f.Message, tc.notNamed)) || (f.FixHint == nil) != (tc.env == "") || (tc.env != "" && f.FixHint.Env != tc.env) {
			t.Errorf("%s: Gate = %+v (hint %+v), want a permanent failure naming %q", tc.provider, f, f.FixHint, tc.names)
		}
		if len(s.runs()) != 0 {
			t.Errorf("%s: a command ran after af --version failed: %v", tc.provider, s.runs())
		}
	}
}
