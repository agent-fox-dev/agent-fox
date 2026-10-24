package afcmd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// boxFixFiles are the files of the repository of these tests: the red fixture, a file to
// delete and a script whose executable bit is to be flipped, and a check of the gate that the
// files the apply step wrote are owned by the user who runs af: the check runs on the tree
// that was applied, before the commit, so a file that a box's user left owned by another
// fails the run where it matters (a podman box), and not after git has rewritten it.
func boxFixFiles() map[string]string {
	files := redFixFiles()
	files["docs/old.txt"] = "old\n"
	files["bin/run.sh"] = "#!/bin/sh\necho run\n"
	files["tests/owner_test.sh"] = "bad=$(find src tests docs bin -not -user \"$(id -un)\" 2>/dev/null | head -3)\n[ -z \"$bad\" ] || { echo \"not owned by the host user: $bad\"; exit 1; }\n"
	return files
}

// shellRound is a round of a brain with hands of its own: it does its work through the
// shell of the box, git among it (which no gate of af would allow it), and then submits.
func shellRound(t *testing.T, script, payload string, hash *string) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		if code, out, err := b.Box.Exec(ctx, script, time.Minute); err != nil || code != 0 {
			t.Errorf("the brain's shell: %d, %v\n%s", code, err, out)
		}
		if hash != nil {
			_, out, _ := b.Box.Exec(ctx, "git rev-parse HEAD", time.Minute)
			*hash = strings.TrimSpace(out)
		}
		out, err := submitting(payload)(ctx, b)
		out.CostSource = brain.CostMeasured
		return out, err
	}
}

// The work of a brain that does the four kinds of change, the fix and a commit of its own.
const (
	fourChanges = `printf 'echo hello\n' > src/greet.sh
cat > tests/hello_test.sh <<'EOF'
[ "$(sh src/greet.sh)" = hello ] || { echo "FAIL: not hello"; exit 1; }
EOF
rm docs/old.txt
chmod +x bin/run.sh`
	commitTheWork = `
git add -A && git commit -q -m 'brain: the work, committed in the box'`
)

// ledgerEntries are the entries of a run's ledger of the kind.
func ledgerEntries(t *testing.T, runDir, kind string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "ledger.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil // a run that recorded nothing has no ledger
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry struct {
			Kind string
			Data map[string]any
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Kind == kind {
			out = append(out, entry.Data)
		}
	}
	return out
}

// TestFixUnderLocalAppliesTheExportOnceAndLandsAllFourChanges_TS_10_32 verifies TS-10-32:
// an edit, an addition, a deletion and a change of the executable bit that a brain made in
// the box all arrive in one commit with the trailer, the commit it made in the box is not
// on the branch, the files are the host user's, and the box was exported once.
// Verifies: 10-REQ-6.1
func TestFixUnderLocalAppliesTheExportOnceAndLandsAllFourChanges_TS_10_32(t *testing.T) {
	requireMake(t)
	fixFourChangesUnder(t, "local")
}

// fixFourChangesUnder runs af fix in the box of the provider with a brain that makes the four
// kinds of change in the box, and a commit of its own, and holds what lands to what 10-REQ-6.1
// says: one commit with the trailer, the four changes in it, the commit of the box not on the
// branch, the files the host user's, and the tree exported once.
func fixFourChangesUnder(t *testing.T, provider string) {
	t.Helper()
	e := newFixEnvWith(t, fakeforge.GitHub, boxFixFiles(), true)
	var inBox string
	spyRounds(t, shellRound(t, fourChanges+commitTheWork, spyReport, &inBox))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", provider)

	if inBox == "" {
		t.Fatal("the brain recorded no commit")
	}
	assertFourChangesLanded(t, e, code, env, inBox)
	if n := len(ledgerEntries(t, runDirOf(e, env), "box_export")); n != 1 {
		t.Errorf("%d box_export entries, want one: the apply step reads the tree through Export once", n)
	}
}

// assertFourChangesLanded holds the result of a run to the landing of the four changes: an
// edit, an addition, a deletion and the executable bit, in one commit with the trailer, the
// commit made in the box (when there is one) not in the repository, and the files owned by
// the host user. It leaves the checkout on main.
func assertFourChangesLanded(t *testing.T, e *fixEnv, code int, env map[string]any, inBox string) {
	t.Helper()
	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	base, _ := res["base_head"].(string)
	if res["outcome"] != "landed" || !strings.HasPrefix(branch, "fix/7-") {
		t.Fatalf("result = %v, want landed on fix/7-…", res)
	}
	if n := len(strings.Fields(gitDo(t, e.dir, "rev-list", base+".."+branch))); n != 1 {
		t.Errorf("%d commits above the base, want one", n)
	}
	if msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Agent-Fox-Run: "+env["run_id"].(string)) {
		t.Errorf("message = %q, want the trailer", msg)
	}
	changes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(gitDo(t, e.dir, "diff", "--raw", "--no-abbrev", base+".."+branch)), "\n") {
		fields := strings.Fields(line) // :old new oldsha newsha status path
		if len(fields) == 7 {
			changes[fields[6]] = fields[4] + fields[0] + ">" + fields[1]
		} else if len(fields) == 6 {
			changes[fields[5]] = fields[4] + fields[0] + ">" + fields[1]
		}
	}
	for path, want := range map[string]string{
		"src/greet.sh":        "M:100644>100644",
		"tests/hello_test.sh": "A:000000>100644",
		"docs/old.txt":        "D:100644>000000",
		"bin/run.sh":          "M:100644>100755",
	} {
		if changes[path] != want {
			t.Errorf("%s: %q in the commit, want %q (all: %v)", path, changes[path], want, changes)
		}
	}
	if inBox != "" {
		if err := exec.Command("git", "-C", e.dir, "cat-file", "-e", inBox+"^{commit}").Run(); err == nil {
			t.Errorf("the commit made in the box, %s, is in the host's repository", inBox)
		}
	}
	e.onBase(t)
	for _, path := range []string{"src/greet.sh", "tests/hello_test.sh", "bin/run.sh"} {
		gitDo(t, e.dir, "checkout", "-q", branch)
		info, err := os.Stat(filepath.Join(e.dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if owner := info.Sys().(*syscall.Stat_t).Uid; int(owner) != os.Getuid() {
			t.Errorf("%s is owned by %d, want the host user %d", path, owner, os.Getuid())
		}
	}
	gitDo(t, e.dir, "checkout", "-q", "main")
}

// TestTheBoxOfTheRunIsInTheLedgerAndTheResult_TS_10_14 verifies TS-10-14: the box_open
// entry of the ledger has the image, the network and the placement, and the result of the
// run says the box: the provider, the image and the network.
// Verifies: 10-REQ-2.6
func TestTheBoxOfTheRunIsInTheLedgerAndTheResult_TS_10_14(t *testing.T) {
	requireMake(t)
	isoboxFor(t)                  // a provider that isolates: the shell of the fix brief is a tool, so its network is open
	isolatingStandIn(t, "podman") // the name that gets an image, over a box that is the local one
	const named = "registry.example/tools:v1"
	for _, tc := range []struct{ name, provider, network, image, imageVar string }{
		{"local", "local", "host", "", ""}, {"isobox", "isobox", "open", "", ""},
		{"podman", "podman", "open", brain.DefaultImage, ""}, {"podman with AF_SANDBOX_IMAGE", "podman", "open", named, named},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			t.Setenv("AF_SANDBOX_IMAGE", tc.imageVar)
			spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))

			code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", tc.provider)

			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			box, _ := resultOf(t, env)["box"].(map[string]any)
			if box["provider"] != tc.provider || box["image"] != tc.image || box["network"] != tc.network || len(box) != 3 {
				t.Errorf("box = %v, want {provider: %s, image: %q, network: %s}", box, tc.provider, tc.image, tc.network)
			}
			entries := ledgerEntries(t, runDirOf(e, env), "box_open")
			if len(entries) != 1 {
				t.Fatalf("%d box_open entries", len(entries))
			}
			for _, key := range []string{"provider", "image", "network", "placement"} {
				if _, ok := entries[0][key]; !ok {
					t.Errorf("box_open = %v, has no %s", entries[0], key)
				}
			}
			if entries[0]["provider"] != tc.provider || entries[0]["network"] != tc.network || entries[0]["image"] != tc.image || entries[0]["placement"] != "host" {
				t.Errorf("box_open = %v, want %s, image %q, network %s, placement host", entries[0], tc.provider, tc.image, tc.network)
			}
		})
	}
}

// standIn notes what a stand-in provider was made and asked for.
type standIn struct {
	builds int
	spec   sandbox.Spec
}

// capturing is the local provider under the name of an isolating one: it notes the spec it is
// asked for and creates without the parts only the real provider can honour.
type capturing struct {
	sandbox.Provider
	in *standIn
}

func (c capturing) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.in.spec = spec
	spec.Image = ""
	spec.Network = sandbox.Network{Mode: sandbox.NetworkUnrestricted}
	return c.Provider.Create(ctx, spec)
}

// isolatingStandIn replaces the provider of the name, for the rest of the test, with the
// local one that says it isolates, so that a run that names podman gets the request it
// computes and no engine is asked for.
func isolatingStandIn(t *testing.T, name string) *standIn {
	t.Helper()
	original, err := box.Default.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	in := &standIn{}
	box.Default.Register(box.Entry{Name: name, Build: func(cfg box.ProviderConfig) (sandbox.Provider, error) {
		in.builds++
		return capturing{Provider: local.Unisolated(), in: in}, nil
	}})
	t.Cleanup(func() { box.Default.Register(original) })
	return in
}

// isoboxFor registers a box provider that is the local one and says it isolates, so that a
// brain that runs in a box is placed in it.
func isoboxFor(t *testing.T) {
	t.Helper()
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	entry := local
	entry.Name, entry.Unconfined = "isobox", false
	box.Default.Register(entry)
}

// TestABrainInABoxIsNotGivenTheStateOfTheRun guards what a round places in the box: the driver
// of AgentKit that runs in the box finds no fix.json in its run directory (it would be the
// round of the host), nor does a brain on the host, and each is told of the tool.
func TestABrainInABoxIsNotGivenTheStateOfTheRun(t *testing.T) {
	requireMake(t)
	isoboxFor(t)
	for _, tc := range []struct {
		name  string
		entry brain.Entry
	}{
		{"the driver in the box", brain.Entry{Name: "spy", DriverInBox: true}},
		{"a brain on the host", brain.Entry{Name: "spy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			var hasState, mentionsTool bool
			var provider string
			s := &spyBrain{}
			s.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
				_, err := fs.Stat(b.Box.FS(ctx), ".agent-fox/run/"+b.RunID+"/fix.json")
				hasState, mentionsTool, provider = err == nil, strings.Contains(b.Task, "submit_fix"), b.Box.Provider()
				return brain.Outcome{StopReason: brain.StopEnded}, nil
			}
			old := testConfig.Brains
			reg := brain.NewRegistry()
			entry := tc.entry
			entry.New = func(brain.Options) (brain.Brain, error) { return s, nil }
			reg.Register(entry)
			testConfig.Brains = reg
			t.Cleanup(func() { testConfig.Brains = old })

			e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "isobox")

			if provider != "isobox" {
				t.Fatalf("the brain ran in the box %q", provider)
			}
			if hasState || !mentionsTool {
				t.Errorf("fix.json in the box: %v, the task names submit_fix: %v; want no fix.json and the tool named", hasState, mentionsTool)
			}
		})
	}
}

// TestTheBoxRequestOfAFixRunSaysWhetherTheProviderWasChosenByDefault guards the flag that
// makes the failure to open a default provider name --sandbox and AF_SANDBOX.
func TestTheBoxRequestOfAFixRunSaysWhetherTheProviderWasChosenByDefault(t *testing.T) {
	local, _ := box.Default.Lookup("local")
	entry, _ := brain.Default.Lookup(brain.ScriptedName)
	dir := t.TempDir()
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &toolio.Run{Ctx: context.Background(), Dir: dir, RunDir: rd}
	for _, source := range []string{brain.SandboxFlag, brain.SandboxEnv, brain.SandboxDefault} {
		req := fixBoxRequest(&testConfig, r, rd, entry, local, &brain.Settings{Sandbox: "local", SandboxSource: source}, brain.Options{})
		if req.Defaulted != (source != brain.SandboxFlag) || req.Provider != "local" || req.RunID != rd.ID || req.Tool != "fix" || req.HostDir != dir {
			t.Errorf("source %s: request = %+v", source, req)
		}
	}
}

// TestTheBrainsAreMadeFromTheIdentityOfTheBuild guards the options of a brain: the model of
// AF_MODEL, and the version and commit that --version prints, so that the af of an image
// that is another can be told.
func TestTheBrainsAreMadeFromTheIdentityOfTheBuild(t *testing.T) {
	t.Setenv("AF_MODEL", "m-test")
	cfg := Config{Version: "v9.9.9", Commit: "c0ffee"}

	opts := cfg.brainOptions()

	if opts.Model != "m-test" || opts.AFVersion != "v9.9.9" || opts.AFCommit != "c0ffee" {
		t.Errorf("options = %+v, want the model of AF_MODEL and the identity of the build", opts)
	}
	supplied := Config{Version: "v9.9.9", Commit: "c0ffee", Brain: brain.Options{Getenv: func(string) (string, bool) { return "", false }}}
	if got := supplied.brainOptions(); got.Model != "" || got.AFVersion != "v9.9.9" {
		t.Errorf("options of a Config whose environment is empty = %+v, want no model and the identity of the build", got)
	}
}
