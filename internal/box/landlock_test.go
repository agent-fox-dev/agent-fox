package box

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// landlockHost sets the operating system and the Landlock ABI that the provider sees,
// and the provider it makes. It returns how often the ABI was read, and the configs
// the provider was made with.
type landlockHost struct {
	abiReads atomic.Int32
	made     []ProviderConfig
}

func hostWithLandlock(t *testing.T, goosName string, abi int, makeProvider func(ProviderConfig) (sandbox.Provider, error)) *landlockHost {
	t.Helper()
	h := &landlockHost{}
	oldGoos, oldABI, oldNew := goos, LandlockABI, newLandlockProvider
	t.Cleanup(func() { goos, LandlockABI, newLandlockProvider = oldGoos, oldABI, oldNew })
	goos = goosName
	LandlockABI = func() int { h.abiReads.Add(1); return abi }
	newLandlockProvider = func(cfg ProviderConfig) (sandbox.Provider, error) {
		h.made = append(h.made, cfg)
		if makeProvider == nil {
			return nil, errors.New("no provider in this test")
		}
		return makeProvider(cfg)
	}
	return h
}

// openLandlock opens a box with the landlock provider of the default registry.
func openLandlock(t *testing.T, extra ...string) (*Session, error) {
	t.Helper()
	return Open(context.Background(), Request{
		Provider: "landlock", HostDir: newRepo(t, 1, "main"), RunID: "20261021_120000_abcdef", Tool: "fix",
		RequireLandlock: true, ExtraRO: extra,
	})
}

// refusedAs says that err is the sandbox failure of the landlock provider, which is
// not retryable and suggests podman.
func refusedAs(t *testing.T, err error, message string) *toolio.Failure {
	t.Helper()
	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || f.Retryable {
		t.Errorf("failure = %+v, want sandbox and not retryable", f)
	}
	if message != "" && !strings.Contains(f.Message, message) {
		t.Errorf("message = %q, want it to contain %q", f.Message, message)
	}
	if f.FixHint == nil || f.FixHint.Flag != "--sandbox" || f.FixHint.Suggest != "podman" || f.FixHint.Env != "" || f.FixHint.Var != "" {
		t.Errorf("fix_hint = %+v, want {flag: --sandbox, suggest: podman}", f.FixHint)
	}
	return f
}

// TestLandlockOnAnotherOSIsRefusedBeforeASandbox_TS_10_15 verifies TS-10-15:
// landlock on an OS other than Linux is failed/sandbox, not retryable, says
// "landlock needs Linux", suggests podman, and creates nothing.
// Verifies: 10-REQ-3.1
func TestLandlockOnAnotherOSIsRefusedBeforeASandbox_TS_10_15(t *testing.T) {
	for _, goosName := range []string{"darwin", "windows", "freebsd"} {
		h := hostWithLandlock(t, goosName, 4, nil)

		s, err := openLandlock(t)

		if s != nil {
			t.Fatalf("%s: a session was returned", goosName)
		}
		f := refusedAs(t, err, "")
		if f.Message != "landlock needs Linux" {
			t.Errorf("%s: message = %q, want %q", goosName, f.Message, "landlock needs Linux")
		}
		if len(h.made) != 0 || h.abiReads.Load() != 0 {
			t.Errorf("%s: the provider was made %d times and the ABI read %d times, want neither", goosName, len(h.made), h.abiReads.Load())
		}
	}
}

// TestTheABIIsReadOnceThroughAReplaceableFunction_TS_10_16 verifies TS-10-16:
// on Linux the provider reads the ABI once through the function variable, and the real
// query returns an integer or the value for no Landlock without panicking.
// Verifies: 10-REQ-3.2
func TestTheABIIsReadOnceThroughAReplaceableFunction_TS_10_16(t *testing.T) {
	h := hostWithLandlock(t, "linux", 4, func(ProviderConfig) (sandbox.Provider, error) { return specRecorder{got: new(sandbox.Spec)}, nil })

	_, err := openLandlock(t, "/opt/af/bin", "/opt/claude")

	if err == nil {
		t.Fatal("Open succeeded with a provider that creates nothing")
	}
	if n := h.abiReads.Load(); n != 1 {
		t.Errorf("the ABI was read %d times, want once", n)
	}
	if len(h.made) != 1 || !slices.Equal(h.made[0].ExtraRO, []string{"/opt/af/bin", "/opt/claude"}) {
		t.Errorf("the provider was made with %+v, want it made once with the directories of the request", h.made)
	}

	if got := landlockABI(); got != -1 && got < 1 {
		t.Errorf("the real query = %d, want an ABI of 1 or more, or -1 for none", got)
	}
}

// TestAnABIBelow4OrNoLandlockIsRefused_TS_10_17 verifies TS-10-17:
// an ABI of 3, and no Landlock, are failed/sandbox, not retryable, name the ABI found
// and the ABI needed, suggest podman and create nothing; an ABI of 4 or more is not
// refused.
// Verifies: 10-REQ-3.3
func TestAnABIBelow4OrNoLandlockIsRefused_TS_10_17(t *testing.T) {
	for _, tc := range []struct {
		abi   int
		found string
	}{{3, "ABI 3"}, {1, "ABI 1"}, {-1, "absent"}, {0, "absent"}} {
		h := hostWithLandlock(t, "linux", tc.abi, nil)

		s, err := openLandlock(t)

		if s != nil {
			t.Fatalf("ABI %d: a session was returned", tc.abi)
		}
		refusedAs(t, err, tc.found)
		f := asFailure(t, err)
		if !strings.Contains(f.Message, "ABI 4") {
			t.Errorf("ABI %d: message %q does not name the ABI that is needed, 4", tc.abi, f.Message)
		}
		if len(h.made) != 0 {
			t.Errorf("ABI %d: the provider was made", tc.abi)
		}
	}
	for _, abi := range []int{4, 5, 6} {
		h := hostWithLandlock(t, "linux", abi, nil)
		_, err := openLandlock(t)
		if len(h.made) != 1 {
			t.Errorf("ABI %d: the provider was made %d times, want once (%v)", abi, len(h.made), err)
		}
	}
}

// TestAProviderThatCannotBeMadeIsASandboxFailure guards the failure of agentkit's own
// provider (no agent binary, for one): it is the same refusal, with the error.
func TestAProviderThatCannotBeMadeIsASandboxFailure(t *testing.T) {
	hostWithLandlock(t, "linux", 4, func(ProviderConfig) (sandbox.Provider, error) {
		return nil, errors.New("agentkit-sandboxd is not beside af or on PATH")
	})

	_, err := openLandlock(t)

	refusedAs(t, err, "agentkit-sandboxd is not beside af or on PATH")
}

// landlockStub is the sandbox provider of agentkit's landlock, as a local one that
// notes the spec it is given and takes the Landlock requirement and the closed
// network off before it creates, which the local provider cannot honour.
type landlockStub struct {
	sandbox.Provider
	got *sandbox.Spec
}

// Capabilities are those of agentkit's landlock provider.
func (landlockStub) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Provider: "landlock", Isolation: sandbox.IsolationConfinement, Landlock: true,
		Network: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted},
	}
}

func (p landlockStub) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	*p.got = spec
	spec.RequireLandlock = false
	spec.Network = sandbox.Network{Mode: sandbox.NetworkUnrestricted}
	return p.Provider.Create(ctx, spec)
}

// TestWithABI4TheSessionSyncsGitInAndExportsWithoutIt_TS_10_18 verifies TS-10-18:
// the provider is given the spec of 10-REQ-2 with RequireLandlock, the tree goes in
// with its .git, comes out without it, and an export that carries .git fails.
// Verifies: 10-REQ-3.4
func TestWithABI4TheSessionSyncsGitInAndExportsWithoutIt_TS_10_18(t *testing.T) {
	var got sandbox.Spec
	f := &faults{}
	c := &counts{}
	hostWithLandlock(t, "linux", 4, func(ProviderConfig) (sandbox.Provider, error) {
		return landlockStub{Provider: faultyProvider{Provider: local.Unisolated(), c: c, f: f}, got: &got}, nil
	})
	dir := newRepo(t, 2, "main")
	if err := os.WriteFile(dir+"/hello.txt", []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "hello")
	l := &ledger{}

	s, err := Open(context.Background(), Request{
		Provider: "landlock", HostDir: dir, RunID: "20261021_120000_abcdef", Tool: "fix", Record: l.record, Warn: l.warn,
		RequireLandlock: true, Network: NetworkNone, Env: map[string]string{"ANTHROPIC_API_KEY": "k"}, Placement: "box",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if s.InPlace() || s.Root() == "" || s.Root() == dir {
		t.Errorf("InPlace() = %v and Root() = %q, want a session that is not in place, with the workspace of the box as its root", s.InPlace(), s.Root())
	}
	if !got.RequireLandlock || got.Network.Mode != sandbox.NetworkNone || got.Env["ANTHROPIC_API_KEY"] != "k" || got.Labels["af.tool"] != "fix" {
		t.Errorf("the spec the provider got = %+v, want RequireLandlock, no network, the key and the labels of the request", got)
	}
	if head := inBox(t, s, "git rev-parse --short HEAD"); head != git(t, dir, "rev-parse", "--short", "HEAD") {
		t.Errorf("HEAD in the box is %s: the tree did not go in with its .git", head)
	}
	var entry map[string]any
	l.last(t, "box_open", &entry)
	if entry["provider"] != "landlock" || entry["network"] != "none" || entry["placement"] != "box" {
		t.Errorf("box_open = %v, want landlock, no network, placement box", entry)
	}

	out, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(out + "/.git"); err == nil {
		t.Error("the export has a .git")
	}
	if data, err := os.ReadFile(out + "/hello.txt"); err != nil || string(data) != "hello\n" {
		t.Errorf("the export has hello.txt = %q, %v", data, err)
	}

	f.exportGit = true
	_, err = s.Export(context.Background())
	fail := asFailure(t, err)
	if fail.Category != toolio.CategorySandbox || !strings.Contains(fail.Message, "export carried .git") {
		t.Errorf("an export that carries .git = %+v, want a sandbox failure that says so", fail)
	}
}
