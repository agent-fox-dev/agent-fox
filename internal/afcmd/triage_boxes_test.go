package afcmd

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// seenByTheBrain is what a brain finds in the box and the brief it is given.
type seenByTheBrain struct {
	provider, network, image string
	env                      map[string]string
	task                     string
	tools                    []string
}

// triageBrainEntry registers the entry as the brain "spy" of the triage loop, whose brain
// notes what it finds and does nothing else.
func triageBrainEntry(t *testing.T, entry brain.Entry, name string) *seenByTheBrain {
	t.Helper()
	seen := &seenByTheBrain{}
	s := &spyBrain{}
	s.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		spec := b.Box.Spec()
		seen.provider, seen.network, seen.image = b.Box.Provider(), b.Box.Network(), b.Box.Image()
		seen.env, seen.task, seen.tools = spec.Env, b.Task, b.Tools
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}
	old := testConfig.Brains
	reg := brain.NewRegistry()
	entry.Name = name
	entry.New = func(brain.Options) (brain.Brain, error) { return s, nil }
	reg.Register(entry)
	testConfig.Brains = reg
	t.Cleanup(func() { testConfig.Brains = old })
	return seen
}

// TestTheTriageBoxIsOpenedWithTheRequestOfTheBrain verifies that the triage loop opens
// its box with the request that brain.BoxRequest makes (10-REQ-2): where the brain runs,
// the credentials in the box, the network for the tools of a triage brief (read-only, so
// that a brain on the host needs none), and the tool the brief names for the submission.
// Verifies: 10-REQ-2.1, 10-REQ-2.2, 10-REQ-2.3
func TestTheTriageBoxIsOpenedWithTheRequestOfTheBrain(t *testing.T) {
	isoboxFor(t)
	const key = "sk-host-key"
	vendor := map[string]string{"ANTHROPIC_API_KEY": key}
	driver := brain.Entry{DriverInBox: true, Credentials: []string{brain.VendorKeyVar}}
	host := brain.Entry{Credentials: []string{brain.VendorKeyVar}}

	for _, tc := range []struct {
		name        string
		entry       brain.Entry
		brainName   string
		args        []string
		wantPlace   string
		wantNetwork string
		wantEnv     map[string]string
	}{
		{"a brain on the host in the local box", host, "spy", []string{"--sandbox", "local"}, "host", "host", nil},
		{"a brain on the host in a box that isolates", host, "spy", []string{"--sandbox", "isobox"}, "host", "none", nil},
		{"the driver in a box that isolates", driver, brain.AgentkitName, []string{"--sandbox", "isobox"}, "box", "open", vendor},
		{"the driver in the local box", driver, brain.AgentkitName, []string{"--sandbox", "local"}, "host", "host", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			t.Setenv("ANTHROPIC_API_KEY", key)
			t.Setenv("GITHUB_TOKEN", "ghp-host")
			seen := triageBrainEntry(t, tc.entry, tc.brainName)

			_, env := e.run("", append([]string{"triage", e.forge.IssueURL(7), "--dir", e.dir, "--brain", tc.brainName, "--land", "none"}, tc.args...)...)

			entries := ledgerEntries(t, e.runDir(env), "box_open")
			if len(entries) != 1 {
				t.Fatalf("%d box_open entries, want one (error %v)", len(entries), env["error"])
			}
			if entries[0]["placement"] != tc.wantPlace || entries[0]["network"] != tc.wantNetwork {
				t.Errorf("box_open = %v, want placement %s and network %s", entries[0], tc.wantPlace, tc.wantNetwork)
			}
			if seen.network != tc.wantNetwork || !mapsEqual(seen.env, tc.wantEnv) {
				t.Errorf("the brain found network %q and env %v, want %q and %v: the key of the vendor for a process, nothing for the host, and never the token of the forge", seen.network, seen.env, tc.wantNetwork, tc.wantEnv)
			}
			if strings.Contains(seen.task, "af triage submit --run-id") || !strings.Contains(seen.task, "file_diagnosis") {
				t.Errorf("the brief does not name the tool file_diagnosis alone:\n%s", seen.task)
			}
			if slices.Contains(seen.tools, "execute") || slices.Contains(seen.tools, "write_file") || !slices.Contains(seen.tools, "read_file") {
				t.Errorf("tools = %v, want the read-only tools of a triage brief", seen.tools)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// TestTheDefaultBoxOfTheCommandsComesFromAFSandbox_TS_10_3 verifies, at the commands, that
// they pin no provider of their own: without --sandbox the box is the one AF_SANDBOX
// names, for af triage and af fix alike.
// Verifies: 10-REQ-1.3
func TestTheDefaultBoxOfTheCommandsComesFromAFSandbox_TS_10_3(t *testing.T) {
	requireMake(t)
	isoboxFor(t)
	t.Setenv("AF_SANDBOX", "isobox")

	te := newTriageEnv(t, fakeforge.GitHub)
	seen := triageBrainEntry(t, brain.Entry{}, "spy")
	_, env := te.run("", "triage", te.forge.IssueURL(7), "--dir", te.dir, "--brain", "spy", "--land", "none")
	if seen.provider != "isobox" {
		t.Errorf("af triage opened the box %q, want isobox from AF_SANDBOX (error %v)", seen.provider, env["error"])
	}
	if opened := ledgerEntries(t, te.runDir(env), "box_open"); len(opened) != 1 || opened[0]["provider"] != "isobox" {
		t.Errorf("box_open = %v, want the provider that AF_SANDBOX named", opened)
	}

	fe := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	var fixProvider string
	s := &spyBrain{run: func(_ context.Context, b brain.Brief) (brain.Outcome, error) {
		fixProvider = b.Box.Provider()
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}}
	useFixSpy(t, s)
	_, env = fe.run("", "fix", fe.forge.IssueURL(7), "--dir", fe.dir, "--brain", "spy", "--land", "none")
	if fixProvider != "isobox" {
		t.Errorf("af fix opened the box %q, want isobox from AF_SANDBOX (error %v)", fixProvider, env["error"])
	}
	if opened := ledgerEntries(t, runDirOf(fe, env), "box_open"); len(opened) != 1 || opened[0]["provider"] != "isobox" {
		t.Errorf("box_open = %v, want the provider that AF_SANDBOX named", opened)
	}

	// With --sandbox the flag wins over the variable.
	fixProvider = ""
	fe.run("", "fix", fe.forge.IssueURL(7), "--dir", fe.dir, "--brain", "spy", "--land", "none", "--sandbox", "local")
	if fixProvider != "local" {
		t.Errorf("af fix --sandbox local opened the box %q, want local", fixProvider)
	}
}

// brokenboxFor registers a box provider that cannot be made, as a default provider whose
// engine is down.
func brokenboxFor(t *testing.T) {
	t.Helper()
	box.Default.Register(box.Entry{Name: "brokenbox", Build: func(box.ProviderConfig) (sandbox.Provider, error) {
		return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: "no engine is running here"}
	}})
}

// TestADefaultBoxThatCannotOpenIsAFailureThatNamesTheFlagAndTheVariable_TS_10_5 verifies,
// at af triage and af fix, 10-REQ-1.3: the provider that AF_SANDBOX names cannot open, so
// the run fails as a sandbox failure whose hint names --sandbox and AF_SANDBOX, and no
// brain runs in another box. With --sandbox the hint is the provider's own.
// Verifies: 10-REQ-1.5
func TestADefaultBoxThatCannotOpenIsAFailureThatNamesTheFlagAndTheVariable_TS_10_5(t *testing.T) {
	requireMake(t)
	brokenboxFor(t)
	t.Setenv("AF_SANDBOX", "brokenbox")
	check := func(what string, code int, env map[string]any, wantHint bool) {
		t.Helper()
		errObj, _ := env["error"].(map[string]any)
		hint, _ := errObj["fix_hint"].(map[string]any)
		if code != 1 || errObj["category"] != "sandbox" || !strings.Contains(errObj["message"].(string), "no engine is running here") {
			t.Errorf("%s: exit code %d, error %v, want the sandbox failure of the provider", what, code, errObj)
		}
		if got := hint["flag"] == "--sandbox" && hint["env"] == "AF_SANDBOX"; got != wantHint {
			t.Errorf("%s: fix_hint = %v, want the flag and the variable: %v", what, hint, wantHint)
		}
	}

	te := newTriageEnv(t, fakeforge.GitHub)
	ran := false
	s := &spyBrain{run: func(context.Context, brain.Brief) (brain.Outcome, error) { ran = true; return brain.Outcome{}, nil }}
	useSpy(t, s)
	code, env := te.run("", "triage", te.forge.IssueURL(7), "--dir", te.dir, "--brain", "spy", "--land", "none")
	check("af triage", code, env, true)

	fe := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	useFixSpy(t, s)
	code, env = fe.run("", "fix", fe.forge.IssueURL(7), "--dir", fe.dir, "--brain", "spy", "--land", "none")
	check("af fix", code, env, true)
	if ran {
		t.Error("a brain ran although the box could not be opened")
	}

	code, env = te.run("", "triage", te.forge.IssueURL(7), "--dir", te.dir, "--brain", "spy", "--land", "none", "--sandbox", "brokenbox")
	check("af triage --sandbox", code, env, false)
}
