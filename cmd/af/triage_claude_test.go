package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fakeclaude"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// useFakeClaude puts a fake claude first on PATH and makes the af that the claude brain
// runs the test binary linked as af, told to do what the script says.
func useFakeClaude(t *testing.T, script fakeclaude.Script) {
	t.Helper()
	bin, af, _ := fakeclaude.Install(t)
	fakeclaude.WriteScript(t, bin, script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ANTHROPIC_API_KEY", "sk-fake-key")
	old := triageBrainOptions
	triageBrainOptions = func() brain.Options {
		opts := baseOptions()
		opts.AFPath = af
		return opts
	}
	t.Cleanup(func() { triageBrainOptions = old })
}

// claudeSubmits is the script of a fake claude that submits the diagnosis through af
// triage submit, as the real one does when the brief tells it to, and ends well with the
// cost and the turns of the table.
func claudeSubmits(payload string) fakeclaude.Script {
	return fakeclaude.Script{
		EnvToStderr: true,
		Commands:    []string{"af triage submit --run-id {run_id} <<'EOF'\n" + payload + "\nEOF"},
		OutputJSON:  `{"type":"result","subtype":"success","is_error":false,"num_turns":5,"total_cost_usd":0.42,"usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":4000},"session_id":"s-1"}`,
	}
}

// submissionOf reads submission.json of a run directory.
func submissionOf(t *testing.T, runDir string) submission.File {
	t.Helper()
	var file submission.File
	if err := json.Unmarshal(readFile(t, filepath.Join(runDir, "submission.json")), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestScriptedAgentkitOnFauxAndAFakeClaudeLeaveTheSameDiagnosis_TS_10_61 verifies TS-10-61:
// af triage on an issue of an httptest forge, in the local box, with the scripted
// brain, the agentkit brain on the faux provider and the claude brain with a fake claude
// first on PATH, leaves the same normalised diagnosis in submission.json for all three,
// and the claude run reports its cost as reported, with the cost and the turns the fake wrote.
// The claude brain also runs in place, under none, where the fake sees the same one
// credential and the diagnosis is the same.
// Verifies: 10-REQ-10.1
func TestScriptedAgentkitOnFauxAndAFakeClaudeLeaveTheSameDiagnosis_TS_10_61(t *testing.T) {
	requireGit(t)
	requireSh(t)
	// A payload whose keys are not in the order of the normal form, so that normalising shows.
	payload := `{"acceptance_criteria":["Run with no arguments prints the usage"],"suggested_fix":"Check the arguments first.","affected_files":[{"note":"Help","path":"util/util.go"},{"path":"main.go","line":6,"note":"Run"}],"root_cause":"Run calls Help before it checks its arguments.","confidence":"probable","severity":"high","title":"Run starts the wrong thing"}`
	files := map[string]submission.File{}
	usages := map[string]map[string]any{}
	for _, run := range []struct{ name, brain, sandbox string }{
		{"scripted", "scripted", "local"}, {"agentkit", "agentkit", "local"}, {"claude", "claude", "local"}, {"claude under none", "claude", "none"},
	} {
		name := run.name
		t.Run(name, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			t.Setenv("GITHUB_TOKEN", "ghp-host-token")
			t.Setenv("GH_TOKEN", "gh-host-token")
			t.Setenv("GITLAB_TOKEN", "gl-host-token")
			args := []string{e.forge.IssueURL(7), "--dir", e.dir, "--land", "none", "--sandbox", run.sandbox, "--brain", run.brain}
			switch run.brain {
			case "scripted":
				args = append(args, "--script", script(t, submitStep(payload, "accepted")))
			case "agentkit":
				fauxBrains(t, faux.New(call("c1", "file_diagnosis", payload)))
			case "claude":
				useFakeClaude(t, claudeSubmits(payload))
			}

			code, env := e.loop(args...)

			if code != 0 || env["status"] != "done" {
				t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
			}
			files[name] = submissionOf(t, e.runDir(env))
			usages[name], _ = env["usage"].(map[string]any)
			if run.brain == "claude" {
				claudeEnvIsTheVendorKeyAndNoForgeToken(t, e, env)
			}
		})
	}

	want := files["scripted"]
	if len(want.Payload) == 0 {
		t.Fatal("the scripted run left no payload")
	}
	for _, name := range []string{"agentkit", "claude", "claude under none"} {
		if string(files[name].Payload) != string(want.Payload) || files[name].PayloadSHA256 != want.PayloadSHA256 {
			t.Errorf("the %s brain left\n%s (%s)\nwant what the scripted brain left\n%s (%s)", name, files[name].Payload, files[name].PayloadSHA256, want.Payload, want.PayloadSHA256)
		}
	}
	if strings.Contains(string(want.Payload), `"acceptance_criteria":["Run`) && strings.Index(string(want.Payload), `"title"`) > strings.Index(string(want.Payload), `"suggested_fix"`) {
		t.Errorf("the payload %s is not in the normal form: title comes after suggested_fix", want.Payload)
	}
	for _, name := range []string{"claude", "claude under none"} {
		u := usages[name]
		if u["brain"] != "claude" || u["cost_source"] != "reported" || u["cost_usd"] != 0.42 || u["turns"] != float64(5) || u["input_tokens"] != float64(5300) || u["output_tokens"] != float64(200) {
			t.Errorf("usage of the %s run = %v, want the cost, turns and tokens the fake wrote, as reported", name, u)
		}
	}
	if usages["scripted"]["cost_source"] != "none" || usages["agentkit"]["cost_source"] != "measured" {
		t.Errorf("cost sources = %v and %v, want none for scripted and measured for agentkit", usages["scripted"]["cost_source"], usages["agentkit"]["cost_source"])
	}
}

// claudeEnvIsTheVendorKeyAndNoForgeToken reads the environment that the fake claude wrote
// to its stderr, which the run copied to the host, and holds it to the one credential.
func claudeEnvIsTheVendorKeyAndNoForgeToken(t *testing.T, e *triageEnv, env map[string]any) {
	t.Helper()
	outputs := artifactsOf(env, "brain_output")
	var stderr string
	for _, a := range outputs {
		if p, _ := a["path"].(string); strings.HasSuffix(p, ".stderr") {
			stderr = string(readFile(t, p))
		}
	}
	if stderr == "" {
		t.Fatalf("no stderr of the claude run among the artifacts %v", outputs)
	}
	if !strings.Contains(stderr, "ANTHROPIC_API_KEY=sk-fake-key") {
		t.Errorf("the environment of claude lacks the vendor key:\n%s", stderr)
	}
	for _, leaked := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "ghp-host-token", "gh-host-token", "gl-host-token"} {
		if strings.Contains(stderr, leaked) {
			t.Errorf("the environment of claude holds %s:\n%s", leaked, stderr)
		}
	}
}

// TestTheClaudeBrainNeedsTheVendorKeyUnlessItRunsAtTheCheckout_TS_10_47 verifies TS-10-47
// at the commands: af triage and af fix with --brain claude and no ANTHROPIC_API_KEY fail
// as auth, naming the variable, before any box is opened, except under --sandbox none,
// where a person at their own checkout may be logged in to Claude Code and the run goes on.
// Verifies: 10-REQ-8.1
func TestTheClaudeBrainNeedsTheVendorKeyUnlessItRunsAtTheCheckout_TS_10_47(t *testing.T) {
	requireGit(t)
	requireSh(t)
	payload := diagnosisPayload(nil)

	te := newTriageEnv(t, fakeforge.GitHub)
	useFakeClaude(t, claudeSubmits(payload))
	t.Setenv("ANTHROPIC_API_KEY", "")
	url := te.forge.IssueURL(7)

	code, env := te.loop(url, "--dir", te.dir, "--land", "none", "--brain", "claude", "--sandbox", "local")
	errObj, _ := env["error"].(map[string]any)
	hint, _ := errObj["fix_hint"].(map[string]any)
	if code != 1 || errObj["category"] != "auth" || hint["env"] != "ANTHROPIC_API_KEY" {
		t.Errorf("af triage under local: exit code %d, error %v, want failed/auth naming ANTHROPIC_API_KEY", code, errObj)
	}
	if _, err := os.Stat(filepath.Join(te.runDir(env), "ledger.jsonl")); err == nil && len(ledgerEntries(t, te.runDir(env), "box_open")) != 0 {
		t.Error("a box was opened for a brain that could not be made")
	}

	code, env = te.loop(url, "--dir", te.dir, "--land", "none", "--brain", "claude", "--sandbox", "none")
	if code != 0 || env["status"] != "done" {
		t.Errorf("af triage under none without the key: exit code %d, status %v, error %v, want the run to go on", code, env["status"], env["error"])
	}

	fe := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	code, env = fe.fix(fe.forge.IssueURL(7), "--brain", "claude", "--sandbox", "local")
	errObj, _ = env["error"].(map[string]any)
	if code != 1 || errObj["category"] != "auth" {
		t.Errorf("af fix under local: exit code %d, error %v, want failed/auth", code, errObj)
	}
	_, env = fe.fix(fe.forge.IssueURL(7), "--brain", "claude", "--sandbox", "none", "--land", "none")
	if errObj, _ = env["error"].(map[string]any); errObj["category"] == "auth" {
		t.Errorf("af fix under none without the key failed as auth: %v", errObj)
	}
}
