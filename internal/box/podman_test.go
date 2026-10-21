package box

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// podmanThatFails is a podman provider whose creation of a sandbox fails with err.
type podmanThatFails struct{ err error }

func (podmanThatFails) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Provider: "podman", Isolation: sandbox.IsolationContainer, Image: true,
		Network: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted},
	}
}

func (p podmanThatFails) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, p.err
}

func (podmanThatFails) Reap(context.Context) ([]string, error) { return nil, nil }

// withPodman sets the operating system and the provider that podman is made as.
func withPodman(t *testing.T, goosName string, makeProvider func() (sandbox.Provider, error)) {
	t.Helper()
	oldGoos, oldNew := goos, newPodmanProvider
	t.Cleanup(func() { goos, newPodmanProvider = oldGoos, oldNew })
	goos, newPodmanProvider = goosName, makeProvider
}

// withPodmanCreateError makes podman a provider whose engine answers err.
func withPodmanCreateError(t *testing.T, goosName, message string) {
	t.Helper()
	withPodman(t, goosName, func() (sandbox.Provider, error) { return podmanThatFails{err: errors.New(message)}, nil })
}

func openPodman(t *testing.T, mutate func(*Request)) (*Session, error) {
	t.Helper()
	req := Request{
		Provider: "podman", HostDir: newRepo(t, 1, "main"), RunID: "20261021_130000_abcdef", Tool: "fix",
		Image: "example.test/img:1", Network: NetworkOpen, Placement: "box",
	}
	if mutate != nil {
		mutate(&req)
	}
	return Open(context.Background(), req)
}

// TestAnEngineThatCannotBeReachedIsASandboxFailure_TS_10_19 verifies TS-10-19:
// an engine that cannot be reached is failed/sandbox, not retryable, carries the
// error of the provider, names --sandbox, and mentions `podman machine start` on an
// operating system other than Linux.
// Verifies: 10-REQ-4.1
func TestAnEngineThatCannotBeReachedIsASandboxFailure_TS_10_19(t *testing.T) {
	for _, tc := range []struct {
		goos    string
		machine bool
	}{{"darwin", true}, {"windows", true}, {"linux", false}} {
		withPodmanCreateError(t, tc.goos, "podman: volume create: cannot connect to Podman socket")

		s, err := openPodman(t, nil)

		if s != nil {
			t.Fatalf("%s: a session was returned", tc.goos)
		}
		f := asFailure(t, err)
		if f.Category != toolio.CategorySandbox || f.Retryable {
			t.Errorf("%s: failure = %+v, want sandbox and not retryable", tc.goos, f)
		}
		if !strings.Contains(f.Message, "cannot connect to Podman socket") {
			t.Errorf("%s: message %q does not carry the error of the provider", tc.goos, f.Message)
		}
		if got := strings.Contains(f.Message, "podman machine start"); got != tc.machine {
			t.Errorf("%s: the message mentions `podman machine start`: %v, want %v (%q)", tc.goos, got, tc.machine, f.Message)
		}
		if f.FixHint == nil || f.FixHint.Flag != "--sandbox" || f.FixHint.Env != "" {
			t.Errorf("%s: fix_hint = %+v, want one that names --sandbox", tc.goos, f.FixHint)
		}
	}
}

// TestAnEngineThatIsNotThereIsTheSameFailure guards the provider that cannot be made
// at all, since there is no podman on PATH: it is the failure of an engine that cannot
// be reached.
func TestAnEngineThatIsNotThereIsTheSameFailure(t *testing.T) {
	withPodman(t, "darwin", func() (sandbox.Provider, error) {
		return nil, errors.New(`exec: "podman": executable file not found in $PATH`)
	})

	_, err := openPodman(t, nil)

	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, "executable file not found") || !strings.Contains(f.Message, "podman machine start") {
		t.Errorf("failure = %+v, want the failure of an engine that cannot be reached, with the error", f)
	}
}

// TestAnImageThatCannotBePulledIsARetryableSandboxFailure_TS_10_20 verifies TS-10-20:
// an image that is missing and cannot be pulled is failed/sandbox, retryable, and the
// message names the image and AF_SANDBOX_IMAGE.
// Verifies: 10-REQ-4.2
func TestAnImageThatCannotBePulledIsARetryableSandboxFailure_TS_10_20(t *testing.T) {
	// One message for each thing the engine says, each of which says one thing only.
	for _, message := range []string{
		"image not found",
		"podman: create: podman run failed with exit status 125; engine stderr: Error: example.test/img:1: image not known",
		"Error: no such image: example.test/img:1",
		"Error: reading manifest latest in example.test/img: manifest unknown",
		"Trying to pull example.test/img:1... Error: name unknown",
		"Error: Pull access denied for example.test/img",
		"Error: unable to pull example.test/img:1: x509: certificate signed by unknown authority",
		"Error: failed to pull example.test/img:1: connection reset by peer",
		`Error: error pulling "example.test/img:1": EOF`,
		"denied: repository does not exist or may require authorization",
		"Error: initializing source docker://example.test/img:1: pinging container registry example.test: no such host",
	} {
		withPodmanCreateError(t, "linux", message)

		_, err := openPodman(t, nil)

		f := asFailure(t, err)
		if f.Category != toolio.CategorySandbox || !f.Retryable {
			t.Errorf("%q: failure = %+v, want sandbox and retryable", message, f)
		}
		if !strings.Contains(f.Message, "example.test/img:1") || !strings.Contains(f.Message, "AF_SANDBOX_IMAGE") {
			t.Errorf("%q: message %q does not name the image and AF_SANDBOX_IMAGE", message, f.Message)
		}
		if strings.Contains(f.Message, "podman machine start") {
			t.Errorf("%q: an image that is missing is not an engine that is down: %q", message, f.Message)
		}
	}
}

// TestADefaultProviderThatCannotOpenNamesHowToChooseAndNeverFallsBack_TS_10_5 verifies
// TS-10-5: when the provider that was chosen by default cannot open, the failure is
// failed/sandbox and its fix_hint names --sandbox and AF_SANDBOX; the other
// providers are never made, and a provider that was asked for does not name the
// variable.
// Verifies: 10-REQ-1.5
func TestADefaultProviderThatCannotOpenNamesHowToChooseAndNeverFallsBack_TS_10_5(t *testing.T) {
	var fallbacks atomic.Int32
	reg := NewRegistry()
	reg.Register(Entry{Name: "podman", Explain: explainPodman, Build: func(ProviderConfig) (sandbox.Provider, error) {
		return nil, errors.New("cannot connect to the engine")
	}})
	for _, name := range []string{"local", "none"} {
		reg.Register(Entry{Name: name, Unconfined: true, New: func() sandbox.Provider { fallbacks.Add(1); return local.Unisolated() }})
	}
	withPodman(t, "darwin", nil)

	s, err := openPodman(t, func(r *Request) { r.Registry, r.Defaulted = reg, true })

	if s != nil {
		t.Fatal("a session was returned")
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || f.FixHint == nil || f.FixHint.Flag != "--sandbox" || f.FixHint.Env != "AF_SANDBOX" {
		t.Errorf("failure = %+v, want sandbox with a fix_hint that names --sandbox and AF_SANDBOX", f)
	}
	if n := fallbacks.Load(); n != 0 {
		t.Errorf("local and none were made %d times, want never", n)
	}

	_, err = openPodman(t, func(r *Request) { r.Registry = reg })
	if f := asFailure(t, err); f.FixHint == nil || f.FixHint.Flag != "--sandbox" || f.FixHint.Env != "" {
		t.Errorf("a provider that was asked for: fix_hint = %+v, want --sandbox alone", f.FixHint)
	}
}

// TestADefaultLandlockThatIsRefusedNamesTheVariableToo guards the hint of a provider
// whose refusal has one already: the suggestion stays, and the variable is added.
func TestADefaultLandlockThatIsRefusedNamesTheVariableToo(t *testing.T) {
	hostWithLandlock(t, "darwin", 4, nil)

	_, err := Open(context.Background(), Request{
		Provider: "landlock", HostDir: newRepo(t, 1, "main"), RunID: "20261021_130000_abcdef", Tool: "fix", Defaulted: true,
	})

	f := asFailure(t, err)
	if f.FixHint == nil || f.FixHint.Flag != "--sandbox" || f.FixHint.Env != "AF_SANDBOX" || f.FixHint.Suggest != "podman" {
		t.Errorf("fix_hint = %+v, want --sandbox, AF_SANDBOX and the suggestion of podman", f.FixHint)
	}
	if f.Message != "landlock needs Linux" {
		t.Errorf("message = %q, want it unchanged", f.Message)
	}
}

// TestOnlyASandboxFailureOfADefaultProviderGetsTheHint guards the hint: a refusal of the
// request itself is not the provider's, and says nothing of choosing another.
func TestOnlyASandboxFailureOfADefaultProviderGetsTheHint(t *testing.T) {
	_, err := Open(context.Background(), Request{Provider: "local", HostDir: t.TempDir(), RunID: "r", Tool: "fix", Defaulted: true})

	f := asFailure(t, err)
	if f.Category != toolio.CategoryUsage || f.FixHint != nil {
		t.Errorf("failure = %+v, want the usage failure of a directory that is no checkout, with no hint", f)
	}
}
