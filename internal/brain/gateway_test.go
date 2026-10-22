package brain

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

const sentinelKey = "sk-ant-sentinel-key-of-the-host"

// gatewayWorld is a fake hub with a lease, a registry whose gateway provider is the one of the
// lease, wrapped so that the commands its boxes run are recorded, and the fake af and claude
// first on the PATH that the gateway's local backend gives its boxes.
type gatewayWorld struct {
	hub *fakehub.Hub
	reg *box.Registry
	af  string // the fake af, for a box whose programs are not called by name
}

func newGatewayWorld(t *testing.T) *gatewayWorld {
	t.Helper()
	bin, af, _ := fakeBin(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := fakehub.New(t)
	h.AddWorkspace(t, fakehub.Workspace{Slug: "acme", PAT: "pat-for-the-brain-tests"})
	lease, err := hub.New(h.URL, "1").Lease(context.Background(), "pat-for-the-brain-tests", "acme", hub.LeaseRequest{Purpose: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	entry := box.GatewayEntry(lease.GatewayURL, lease.Token)
	build := entry.Build
	entry.Build = func(cfg box.ProviderConfig) (sandbox.Provider, error) {
		p, err := build(cfg)
		if err != nil {
			return nil, err
		}
		return h.Gateway().Instrument(p), nil
	}
	reg := box.NewRegistry()
	reg.Register(entry)
	return &gatewayWorld{hub: h, reg: reg, af: af}
}

// TestABrainInAGatewayBoxNeedsNoHostKeyAndNamesItsCredentialsByReference_TS_13_45 verifies
// TS-13-45: with a lookup of the environment that yields nothing, while the process holds the
// key, the brains that run in a box start in a gateway box without a key; a process they start
// there is told the name of the key as a reference to the box's own environment, and the
// command holds the reference and not the value. A box whose environment is not its own is
// told no name that its spec does not hold, and a brain there still needs the key.
// Verifies: 13-REQ-6.5
func TestABrainInAGatewayBoxNeedsNoHostKeyAndNamesItsCredentialsByReference_TS_13_45(t *testing.T) {
	t.Setenv(VendorKeyVar, sentinelKey)
	w := newGatewayWorld(t)
	opts := Options{Getenv: noEnv, Provider: box.GatewayName, Boxes: w.reg, AFVersion: "v1.0.0", AFCommit: "abc123"}

	if _, err := newClaude(opts); err != nil {
		t.Errorf("the claude brain in a gateway box needs no host key: %v", err)
	}
	// Nor does it need claude on this machine: the image has it, and it is called by name.
	t.Run("no claude on the host", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := newClaude(opts); err != nil {
			t.Errorf("the claude brain in a gateway box needs claude on the host: %v", err)
		}
		if _, err := newClaude(Options{Getenv: noEnv, Provider: "local"}); err == nil {
			t.Error("the claude brain under local started with no claude on the host")
		}
	})
	if _, err := newAgentkitInBox(opts); err != nil {
		t.Errorf("the agentkit brain in a gateway box needs no host key: %v", err)
	}
	if !driverInBox(opts) {
		t.Error("the agentkit brain does not run in the gateway box: it isolates")
	}
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	if local.SuppliesCredentials {
		t.Fatal("the local provider supplies credentials")
	}
	noKey := Options{Getenv: noEnv, Provider: "local"}
	if _, err := newClaude(noKey); err == nil {
		t.Error("the claude brain under local started without a key")
	}
	if _, err := newAgentkitInBox(noKey); err == nil {
		t.Error("the agentkit brain in a local box started without a key")
	}

	dir := newRepo(t)
	rd := newRunDir(t, dir)
	s, err := box.Open(context.Background(), box.Request{Provider: box.GatewayName, HostDir: dir, RunID: rd.ID, Tool: "fix", Registry: w.reg, Record: rd.Record, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if len(w.hub.Gateway().Created()[0].Spec.Env) != 0 {
		t.Errorf("the box was created with the environment %v", w.hub.Gateway().Created()[0].Spec.Env)
	}

	run := func(sess *box.Session, spec ProcessSpec) {
		t.Helper()
		b := Brief{RunID: rd.ID, Attempt: 1, Box: sess, RunDir: rd.Path, Timeout: time.Minute, Warn: func(string, string) {}}
		cfg := opts.ProcessConfig()
		cfg.AFPath = w.af
		if _, err := NewProcess(cfg).Run(context.Background(), b, spec); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	spec := ProcessSpec{Brain: ClaudeName, Program: ProgramAF, Args: []string{"--version"}, Credentials: []string{VendorKeyVar}, Probes: []string{ProgramAF}}
	run(s, spec)

	var holding []string
	for _, c := range w.hub.Gateway().Commands() {
		if strings.Contains(c.Command, sentinelKey) {
			t.Errorf("a command holds the value of the key: %s", c.Command)
		}
		if strings.Contains(c.Command, `ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"`) {
			holding = append(holding, c.Command)
		}
	}
	if len(holding) != 1 || !strings.Contains(holding[0], "env -i") || !strings.Contains(holding[0], `PATH="$PATH"`) || !strings.Contains(holding[0], `'af' '--version'`) {
		t.Errorf("commands that name the key by reference: %q, want the one of the process, run by name under env -i", holding)
	}

	// A box that does not supply its credentials names the ones its spec holds, and no other.
	reg := box.NewRegistry()
	plain := local
	plain.Name = "plain"
	plain.New = func() sandbox.Provider { return w.hub.Gateway().Instrument(localProvider()) }
	reg.Register(plain)
	ps, err := box.Open(context.Background(), box.Request{Provider: "plain", HostDir: newRepo(t), RunID: rd.ID, Tool: "fix", Registry: reg, Record: rd.Record, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	before := len(w.hub.Gateway().Commands())
	run(ps, ProcessSpec{Brain: ClaudeName, Program: ProgramAF, Args: []string{"--version"}, Credentials: []string{VendorKeyVar}, Probes: []string{ProgramAF}})
	for _, c := range w.hub.Gateway().Commands()[before:] {
		if strings.Contains(c.Command, VendorKeyVar) {
			t.Errorf("a box that supplies no credentials of its own was told to refer to %s: %s", VendorKeyVar, c.Command)
		}
	}
}

// localProvider is the local provider, the backend of the box that is not a gateway's.
func localProvider() sandbox.Provider { return local.Unisolated() }

// TestTheProbeIsSharedByEveryRunnerOfASession verifies that two process runners of one box probe
// af once between them, as the measuring of a fix and a brain do.
func TestTheProbeIsSharedByEveryRunnerOfASession(t *testing.T) {
	w := newGatewayWorld(t)
	opts := Options{Getenv: noEnv, Provider: box.GatewayName, Boxes: w.reg, AFVersion: "v1.0.0", AFCommit: "abc123"}
	dir := newRepo(t)
	rd := newRunDir(t, dir)
	s, err := box.Open(context.Background(), box.Request{Provider: box.GatewayName, HostDir: dir, RunID: rd.ID, Tool: "fix", Registry: w.reg, Record: rd.Record, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	b := Brief{RunID: rd.ID, Attempt: 1, Box: s, RunDir: rd.Path, Timeout: time.Minute, Warn: func(string, string) {}}
	for range 2 {
		spec := ProcessSpec{Brain: "ring1", Program: ProgramAF, Args: []string{"--version"}, Probes: []string{ProgramAF}}
		if _, err := NewProcess(opts.ProcessConfig()).Run(context.Background(), b, spec); err != nil {
			t.Fatal(err)
		}
		b.Attempt++
	}
	probes := 0
	for _, c := range w.hub.Gateway().Commands() {
		if strings.HasSuffix(strings.TrimSpace(c.Command), `'af' '--version'`) {
			probes++
		}
	}
	if probes != 1 {
		t.Errorf("af --version was probed %d times by two runners, want once for the box", probes)
	}
}

// TestTheClaudeBrainRunsInAGatewayBoxWithItsKeyByReference verifies the claude brain end to end in
// a gateway box: no key on the host, the process told the name of the key and not its value,
// and claude run by name in the box.
func TestTheClaudeBrainRunsInAGatewayBoxWithItsKeyByReference(t *testing.T) {
	t.Setenv(VendorKeyVar, sentinelKey)
	w := newGatewayWorld(t)
	_, af, claude := fakeBin(t)
	opts := Options{Getenv: noEnv, Provider: box.GatewayName, Boxes: w.reg, AFVersion: "v1.0.0", AFCommit: "abc123"}
	entry, err := Default.Lookup(ClaudeName)
	if err != nil {
		t.Fatal(err)
	}
	gw, err := w.reg.Lookup(box.GatewayName)
	if err != nil {
		t.Fatal(err)
	}
	dir := newRepo(t)
	rd := newRunDir(t, dir)
	req := BoxRequest(BoxParams{
		Brain: entry, Provider: gw, Getenv: noEnv, Tools: []string{"read_file", "list_files"}, HostDir: dir, RunID: rd.ID, Tool: "fix",
		Record: rd.Record, Warn: func(string, string) {}, Registry: w.reg,
	})
	if len(req.Env) != 0 {
		t.Fatalf("the request for the box holds the environment %v", req.Env)
	}
	s, err := box.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	b, err := Default.New(ClaudeName, opts)
	if err != nil {
		t.Fatalf("the claude brain in a gateway box: %v", err)
	}
	cw := &claudeWorld{procWorld: &procWorld{world: &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}, af: af, claude: claude}, brain: b}

	if _, err := cw.runClaude(1, nil); err != nil {
		t.Fatal(err)
	}

	if runs := cw.runsOf(ProgramClaude); len(runs) != 1 {
		t.Fatalf("claude ran %d times in the box", len(runs))
	}
	var holding int
	for _, c := range w.hub.Gateway().Commands() {
		if strings.Contains(c.Command, sentinelKey) {
			t.Errorf("a command holds the value of the key: %s", c.Command)
		}
		if strings.Contains(c.Command, `ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"`) && strings.Contains(c.Command, `'claude'`) {
			holding++
		}
	}
	if holding != 1 {
		t.Errorf("%d commands run claude with the key by reference, want one", holding)
	}
}

// TestTheProcessesOfTheBrainsNameTheVendorKeyAndHoldNoValue pins what each brain asks of a box that
// supplies the credentials: the name of the vendor key, as the one credential, and nothing that
// holds a value of it.
// Verifies: 13-REQ-6.5
func TestTheProcessesOfTheBrainsNameTheVendorKeyAndHoldNoValue(t *testing.T) {
	t.Setenv(VendorKeyVar, sentinelKey)
	for name, spec := range map[string]ProcessSpec{
		"agentkit": agentkitProcess([]string{"fix", "run"}, map[string]string{"AF_MODEL": "m1"}),
		"claude":   claudeProcess([]string{"-p"}),
	} {
		if !slices.Equal(spec.Credentials, []string{VendorKeyVar}) {
			t.Errorf("%s: credentials = %v, want the name of the vendor key alone", name, spec.Credentials)
		}
		if strings.Contains(strings.Join(spec.Args, " ")+fmt.Sprint(spec.Inline), sentinelKey) {
			t.Errorf("%s: the spec holds the value of the key", name)
		}
	}
}
