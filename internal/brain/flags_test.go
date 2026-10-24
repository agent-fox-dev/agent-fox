package brain

import (
	"context"
	"flag"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// providerNames are the providers of the box, in the order the message lists them.
var providerNames = []string{"local", "podman"}

// recordingRegistry holds the providers as entries that are the local provider
// underneath, and counts how often a provider is made: a box is opened by it.
func recordingRegistry() (*box.Registry, *atomic.Int32) {
	made := &atomic.Int32{}
	r := box.NewRegistry()
	for _, name := range providerNames {
		r.Register(box.Entry{Name: name, Unconfined: name == "local", New: func() sandbox.Provider {
			made.Add(1)
			return local.Unisolated()
		}})
	}
	return r, made
}

// resolve parses args as a command that pins no default sandbox does, on a host with
// the environment given, and returns the settings with the error of Validate.
func resolve(t *testing.T, reg *box.Registry, env map[string]string, args ...string) (*Settings, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(discard{})
	s := Flags(fs, Defaults{Budget: 5, MaxTurns: 40})
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	s.registry = reg
	return s, s.ValidateWith(func(name string) (string, bool) { v, ok := env[name]; return v, ok })
}

// namesAll says that the message lists every provider.
func namesAll(t *testing.T, message string) {
	t.Helper()
	for _, name := range providerNames {
		if !strings.Contains(message, name) {
			t.Errorf("the message %q does not list %s", message, name)
		}
	}
}

// TestAnUnknownSandboxIsUsageListingThemAll_TS_10_2 verifies TS-10-2:
// --sandbox docker is refused as usage, with every name, before any box.
// Verifies: 10-REQ-1.2
func TestAnUnknownSandboxIsUsageListingThemAll_TS_10_2(t *testing.T) {
	reg, made := recordingRegistry()

	_, err := resolve(t, reg, nil, "--sandbox", "docker")

	if err == nil {
		t.Fatal("--sandbox docker was accepted")
	}
	usageNaming(t, err, "--sandbox")
	namesAll(t, err.Error())
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("the message %q does not name the value that was refused", err)
	}
	if n := made.Load(); n != 0 {
		t.Errorf("%d providers were made while the flags were resolved", n)
	}
}

// TestWithoutSandboxTheProviderIsAFSandboxElsePodman_TS_10_3 verifies TS-10-3:
// --sandbox, then AF_SANDBOX, then podman, on every operating system; and the name that
// was selected is the one the box_open entry of the ledger carries.
// Verifies: 10-REQ-1.3
func TestWithoutSandboxTheProviderIsAFSandboxElsePodman_TS_10_3(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
		want string
		by   string
	}{
		{"the default", nil, nil, "podman", SandboxDefault},
		{"AF_SANDBOX local", map[string]string{"AF_SANDBOX": "local"}, nil, "local", SandboxEnv},
		{"AF_SANDBOX podman", map[string]string{"AF_SANDBOX": "podman"}, nil, "podman", SandboxEnv},
		{"--sandbox beats AF_SANDBOX", map[string]string{"AF_SANDBOX": "local"}, []string{"--sandbox", "podman"}, "podman", SandboxFlag},
		{"--sandbox beats the default", nil, []string{"--sandbox", "local"}, "local", SandboxFlag},
		{"an empty AF_SANDBOX is unset", map[string]string{"AF_SANDBOX": ""}, nil, "podman", SandboxDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, _ := recordingRegistry()

			s, err := resolve(t, reg, tc.env, tc.args...)

			if err != nil {
				t.Fatalf("Validate = %v", err)
			}
			if s.Sandbox != tc.want || s.SandboxSource != tc.by {
				t.Errorf("sandbox = %q chosen by %q, want %q chosen by %q", s.Sandbox, s.SandboxSource, tc.want, tc.by)
			}
			if got := openedProvider(t, reg, s.Sandbox); got != tc.want {
				t.Errorf("the box_open entry says %q, want %q", got, tc.want)
			}
		})
	}
}

// openedProvider opens a box with the provider of the name, and returns the provider
// that its box_open entry carries.
func openedProvider(t *testing.T, reg *box.Registry, name string) string {
	t.Helper()
	var provider string
	record := func(kind string, data any) error {
		if kind == "box_open" {
			provider, _ = data.(map[string]any)["provider"].(string)
		}
		return nil
	}
	s, err := box.Open(context.Background(), box.Request{
		Provider: name, HostDir: newRepo(t), RunID: "20261021_090000_abcdef", Tool: "fix", Record: record, Registry: reg,
	})
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return provider
}

// TestAnInvalidAFSandboxIsUsageNamingTheVariable_TS_10_4 verifies TS-10-4:
// AF_SANDBOX=bogus with no --sandbox is refused as usage, naming the variable and
// every provider, before any box.
// Verifies: 10-REQ-1.4
func TestAnInvalidAFSandboxIsUsageNamingTheVariable_TS_10_4(t *testing.T) {
	reg, made := recordingRegistry()

	_, err := resolve(t, reg, map[string]string{"AF_SANDBOX": "bogus"})

	if err == nil {
		t.Fatal("AF_SANDBOX=bogus was accepted")
	}
	usageNaming(t, err, "AF_SANDBOX")
	namesAll(t, err.Error())
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("the message %q does not name the value that was refused", err)
	}
	if n := made.Load(); n != 0 {
		t.Errorf("%d providers were made while the flags were resolved", n)
	}

	// The criterion has no exception for a run that says --sandbox: a variable that is
	// wrong is wrong, and is not left to surface on the day the flag is dropped.
	_, err = resolve(t, reg, map[string]string{"AF_SANDBOX": "bogus"}, "--sandbox", "local")
	if err == nil {
		t.Fatal("AF_SANDBOX=bogus was accepted with --sandbox local")
	}
	usageNaming(t, err, "AF_SANDBOX")
	namesAll(t, err.Error())
}

// TestACommandMayPinItsDefaultSandbox guards the default of a command that names its
// own: it is used before AF_SANDBOX and the default, and --sandbox still wins over it.
func TestACommandMayPinItsDefaultSandbox(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{nil, "local"}, {[]string{"--sandbox", "podman"}, "podman"}} {
		reg, _ := recordingRegistry()
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		s := Flags(fs, Defaults{Sandbox: "local", Budget: 5, MaxTurns: 40})
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		s.registry = reg

		err := s.ValidateWith(func(name string) (string, bool) { return "podman", name == "AF_SANDBOX" })

		if err != nil || s.Sandbox != tc.want {
			t.Errorf("%v: sandbox = %q, Validate = %v, want %q whatever AF_SANDBOX says", tc.args, s.Sandbox, err, tc.want)
		}
	}
}
