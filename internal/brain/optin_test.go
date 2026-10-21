package brain

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/fakeclaude"
	"github.com/agent-fox-dev/agentfox/internal/optin"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// The tests of this file are opt-in: a real box, or the real Claude Code, is more than
// make check has. Each gate (internal/optin) skips its test with the variable or the
// prerequisite it names, except for the local box, which every machine has.

// openBox opens the box that the brain would get on the provider, from the host
// environment as it is, over the repository.
func openBox(t *testing.T, brainName, provider string, tools []string, dir string, rd *toolio.RunDir) *box.Session {
	t.Helper()
	entry, err := Default.Lookup(brainName)
	if err != nil {
		t.Fatal(err)
	}
	providerEntry, err := box.Default.Lookup(provider)
	if err != nil {
		t.Fatal(err)
	}
	s, err := box.Open(context.Background(), BoxRequest(BoxParams{
		Brain: entry, Provider: providerEntry, Getenv: os.LookupEnv, Tools: tools,
		HostDir: dir, RunID: rd.ID, Tool: "triage", Record: rd.Record, Warn: func(string, string) {},
	}))
	if err != nil {
		t.Fatalf("the %s box does not open: %v", provider, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// podmanImage is the image that a podman box of this environment runs.
func podmanImage() string {
	if v := os.Getenv("AF_SANDBOX_IMAGE"); v != "" {
		return v
	}
	return DefaultImage
}

// boxInvariants holds a box of the provider to what spec 07 promises of every box, with
// the credentials of the forge and of the vendor in the environment of the host: .git goes
// in and never comes out, the host is untouched by what is done in the box, no credential is
// in it, a file is written only under .agent-fox/, and the tree comes out as it was
// left: an edit, an addition, a deletion and a change of the executable bit.
func boxInvariants(t *testing.T, provider string) {
	t.Helper()
	dir := newRepo(t)
	for name, mode := range map[string]os.FileMode{"bin/run.sh": 0o755, "docs/old.txt": 0o644} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "remote", "add", "origin", "https://user:ghp_REMOTE_SECRET@example.com/x.git")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "files to change")
	hostHead := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	secrets := map[string]string{"GITHUB_TOKEN": "ghp-host-token", "GH_TOKEN": "gh-host-token", "GITLAB_TOKEN": "gl-host-token", "ANTHROPIC_API_KEY": "sk-host-key"}
	for name, value := range secrets {
		t.Setenv(name, value)
	}
	rd := newRunDir(t, dir)
	s := openBox(t, ScriptedName, provider, []string{"read_file"}, dir, rd)
	run := func(command string) (int, string) {
		t.Helper()
		code, out, err := s.Exec(context.Background(), command, time.Minute)
		if err != nil {
			t.Fatalf("%s: %v", command, err)
		}
		return code, out
	}

	// .git goes in.
	if code, out := run("git rev-parse HEAD"); code != 0 || strings.TrimSpace(out) != hostHead {
		t.Errorf("the box's HEAD = %q (exit %d), want the host's %s: .git goes in", out, code, hostHead)
	}
	// The only credential in the box is none: the brain is on the host, so nothing is given.
	_, env := run("env; git config --local --list")
	for name, value := range secrets {
		if strings.Contains(env, name) || strings.Contains(env, value) {
			t.Errorf("the box holds %s: %s", name, env)
		}
	}
	if strings.Contains(env, "REMOTE_SECRET") {
		t.Errorf("the box's git configuration holds the token of the remote:\n%s", env)
	}

	// Work in the box, a commit among it.
	work := `printf 'changed\n' > README.md && printf 'new\n' > docs/new.txt && rm docs/old.txt && chmod +x src/main.go &&
git add -A && git -c user.name=box -c user.email=box@example.com commit -q -m 'made in the box'`
	if code, out := run(work); code != 0 {
		t.Fatalf("the work in the box: exit %d\n%s", code, out)
	}
	if got := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD")); got != hostHead {
		t.Errorf("the host's HEAD is %s after work in the box, want %s", got, hostHead)
	}
	if status := git(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the host's tree changed while the box worked:\n%s", status)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(data) != "# x\n" {
		t.Errorf("README.md of the host = %q, want it as it was", data)
	}

	// The tree comes out as it was left, and without .git.
	exported, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(exported, ".git")); err == nil {
		t.Error(".git came out of the box")
	}
	if data, _ := os.ReadFile(filepath.Join(exported, "README.md")); string(data) != "changed\n" {
		t.Errorf("README.md of the export = %q, want the edit", data)
	}
	if data, _ := os.ReadFile(filepath.Join(exported, "docs", "new.txt")); string(data) != "new\n" {
		t.Errorf("docs/new.txt of the export = %q, want the addition", data)
	}
	if _, err := os.Lstat(filepath.Join(exported, "docs", "old.txt")); err == nil {
		t.Error("docs/old.txt is in the export, but the box deleted it")
	}
	if info, err := os.Stat(filepath.Join(exported, "src", "main.go")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("src/main.go of the export = %v, %v, want the executable bit the box set", info, err)
	}

	// A file is written only under .agent-fox/, and the view of the tree is read-only.
	ctx := context.Background()
	if err := s.WriteFile(ctx, "src/planted.go", []byte("package main\n")); err == nil {
		t.Error("WriteFile wrote outside .agent-fox/")
	}
	if err := s.WriteFile(ctx, ".agent-fox/run/"+rd.ID+"/note.txt", []byte("noted")); err != nil {
		t.Errorf("WriteFile under .agent-fox/: %v", err)
	}
	data, err := fs.ReadFile(s.FS(ctx), ".agent-fox/run/"+rd.ID+"/note.txt")
	if err != nil || string(data) != "noted" {
		t.Errorf("the file written under .agent-fox/ reads back as %q, %v", data, err)
	}
	if f, err := s.FS(ctx).Open("README.md"); err == nil {
		if w, ok := f.(interface{ Write([]byte) (int, error) }); ok {
			if _, werr := w.Write([]byte("x")); werr == nil {
				t.Error("the view of the tree can be written")
			}
		}
		_ = f.Close()
	}
}

// TestTheBoxInvariantsHoldUnderTheLocalBox runs the invariants of the opt-in tests
// under the one box every machine has, so that make check proves the checks themselves.
func TestTheBoxInvariantsHoldUnderTheLocalBox(t *testing.T) {
	boxInvariants(t, "local")
}

// TestSpec07sBoxInvariantsHoldUnderTheRealBoxes_TS_10_63 verifies, for the real
// providers, what TS-10-63 asks: spec 07's invariants of a box hold under landlock and
// under podman. It runs where AF_TEST_LANDLOCK or AF_TEST_PODMAN says so, and is skipped
// with the reason otherwise.
// Verifies: 10-REQ-10.3
func TestSpec07sBoxInvariantsHoldUnderTheRealBoxes_TS_10_63(t *testing.T) {
	t.Run("landlock", func(t *testing.T) {
		optin.RequireLandlock(t)
		boxInvariants(t, "landlock")
	})
	t.Run("podman", func(t *testing.T) {
		optin.RequirePodman(t, podmanImage())
		boxInvariants(t, "podman")
	})
}

// realClaudeTask is the task of the contract row for the real model: it is told to try
// the one thing the hook refuses, and then to submit a diagnosis that is given in full, so
// that what the run shows is the contract and not the model's skill.
const realClaudeTask = `## Task

This is run %s.

First try, once, to run ` + "`git commit --allow-empty -m probe`" + `; the run refuses that, which is expected.
Then submit this diagnosis by running ` + "`af triage submit --run-id %s`" + ` with exactly this JSON on its stdin,
and stop:

` + "```json\n" + `{"title":"The readme is a stub","severity":"low","confidence":"probable","root_cause":"README.md holds a title and nothing else.","affected_files":[{"path":"README.md","note":"the stub"}],"suggested_fix":"Describe what the project does.","acceptance_criteria":["README.md describes the project"]}` + "\n```\n"

// realClaudeDiagnosis is the diagnosis that the task gives in full.
const realClaudeDiagnosis = `{"title":"The readme is a stub","severity":"low","confidence":"probable","root_cause":"README.md holds a title and nothing else.","affected_files":[{"path":"README.md","note":"the stub"}],"suggested_fix":"Describe what the project does.","acceptance_criteria":["README.md describes the project"]}`

// stubClaude puts a fake claude first on PATH, told to do what the model is told to do:
// try a commit, which nothing stops in a box that is a copy, and submit the diagnosis
// through the real af. It lets the test of the real claude be proven where there is none.
func stubClaude(t *testing.T) {
	t.Helper()
	bin, _, _ := fakeBin(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fakeclaude.WriteScript(t, bin, fakeScript{
		Commands:   []string{"git -c user.name=m -c user.email=m@example.com commit -q --allow-empty -m probe", "af triage submit --run-id {run_id} <<'EOF'\n" + realClaudeDiagnosis + "\nEOF"},
		OutputJSON: claudeResultJSON(0.42, 5),
	})
}

// realClaudeContract runs the claude brain over a box of the provider through Execute: the
// diagnosis is submitted and accepted, the run reports its cost as reported by the
// vendor, and nothing was committed on the host. With stub, the claude is a fake one
// (see stubClaude).
func realClaudeContract(t *testing.T, provider string, stub bool) {
	t.Helper()
	dir := newRepo(t)
	if stub {
		stubClaude(t)
		t.Setenv("ANTHROPIC_API_KEY", "sk-stub")
	}
	hostHead := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	rd := newRunDir(t, dir)
	tools := []string{"read_file", "list_files", "find_files", "search_files"}
	s := openBox(t, ClaudeName, provider, tools, dir, rd)
	opts := Options{Provider: provider, Getenv: os.LookupEnv, Model: os.Getenv("AF_MODEL"), AFPath: realAF(t)}
	b, err := Default.New(ClaudeName, opts)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}
	brief := Brief{
		RunID: rd.ID, Attempt: 1, Kind: triage.Kind(dir, s.Root()), Prefix: "You are diagnosing a made-up problem in a small repository.\n\n",
		Task:  strings.ReplaceAll(realClaudeTask, "%s", rd.ID),
		Tools: tools, Hook: HookConfig{RunID: rd.ID, ProtectedPaths: []string{".agent-fox/", ".specs/"}},
		MaxCostUSD: 1, MaxTurns: 12, Timeout: 5 * time.Minute,
	}
	req := w.request(brief, b)
	req.Name = ClaudeName

	res, err := Execute(context.Background(), req)

	if err != nil || !res.Outcome.Submitted {
		for _, rel := range []string{fakeclaude.CommandsPath, fakeclaude.RecordPath} {
			data, _ := w.boxFile(rel)
			t.Logf("%s: %s", rel, data)
		}
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	var file submission.File
	if data, ok := w.hostSubmission(); !ok || json.Unmarshal([]byte(data), &file) != nil || !strings.Contains(string(file.Payload), "The readme is a stub") {
		t.Errorf("the submission of the host = %q, want the diagnosis that was given", data)
	}
	u := w.report.lastUsage()
	if u.CostSource != CostReported || u.CostUSD <= 0 || u.Turns < 1 || u.Brain != ClaudeName || (stub && (u.CostUSD != 0.42 || u.Turns != 5)) {
		t.Errorf("usage = %+v, want the cost the vendor reported, with turns", u)
	}
	if got := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD")); got != hostHead {
		t.Errorf("HEAD of the host moved from %s to %s: the brain's commit got out", hostHead, got)
	}
}

// TestTheContractRowOfTheRealClaude_TS_10_63 verifies, for the real Claude Code, what
// TS-10-63 asks: the contract row passes under the local box, and under podman when both
// AF_TEST_CLAUDE and AF_TEST_PODMAN are set.
// Verifies: 10-REQ-10.3
func TestTheContractRowOfTheRealClaude_TS_10_63(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		optin.RequireClaude(t)
		realClaudeContract(t, "local", false)
	})
	t.Run("podman", func(t *testing.T) {
		optin.RequireClaude(t)
		optin.RequirePodman(t, podmanImage())
		realClaudeContract(t, "podman", false)
	})
}

// TestTheContractRowOfTheRealClaudeRunsWithAFakeOne runs the test of the real Claude Code
// with a fake one, so that make check proves what the test asks and asserts: the task, the
// kind and the tools, the submission through the real af, the usage and the host's HEAD.
func TestTheContractRowOfTheRealClaudeRunsWithAFakeOne(t *testing.T) {
	realClaudeContract(t, "local", true)
}
