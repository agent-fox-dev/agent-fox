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

// providerNames are the four providers of the box, in the order the message lists them.
var providerNames = []string{"landlock", "local", "none", "podman"}

// recordingRegistry holds the four providers as entries that are the local provider
// underneath, and counts how often a provider is made: a box is opened by it.
func recordingRegistry() (*box.Registry, *atomic.Int32) {
	made := &atomic.Int32{}
	r := box.NewRegistry()
	for _, name := range providerNames {
		r.Register(box.Entry{Name: name, Unconfined: name == "local" || name == "none", InPlace: name == "none", New: func() sandbox.Provider {
			made.Add(1)
			return local.Unisolated()
		}})
	}
	return r, made
}

// resolve parses args as a command that pins no default sandbox does, on a host with
// the operating system and the environment given, and returns the settings with the
// error of Validate.
func resolve(t *testing.T, reg *box.Registry, goosName string, env map[string]string, args ...string) (*Settings, error) {
	t.Helper()
	old := box.GOOS
	box.GOOS = goosName
	t.Cleanup(func() { box.GOOS = old })
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

// TestAnUnknownSandboxIsUsageListingAllFour_TS_10_2 verifies TS-10-2:
// --sandbox docker is refused as usage, with all four names, before any box.
// Verifies: 10-REQ-1.2
func TestAnUnknownSandboxIsUsageListingAllFour_TS_10_2(t *testing.T) {
	reg, made := recordingRegistry()

	_, err := resolve(t, reg, "linux", nil, "--sandbox", "docker")

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

// TestWithoutSandboxTheProviderIsAFSandboxElseTheOSDefault_TS_10_3 verifies TS-10-3:
// --sandbox, then AF_SANDBOX, then landlock on Linux and podman anywhere else; and the
// name that was selected is the one the box_open entry of the ledger carries.
// Verifies: 10-REQ-1.3
func TestWithoutSandboxTheProviderIsAFSandboxElseTheOSDefault_TS_10_3(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		env  map[string]string
		args []string
		want string
		by   string
	}{
		{"linux", "linux", nil, nil, "landlock", SandboxDefault},
		{"darwin", "darwin", nil, nil, "podman", SandboxDefault},
		{"windows", "windows", nil, nil, "podman", SandboxDefault},
		{"freebsd", "freebsd", nil, nil, "podman", SandboxDefault},
		{"AF_SANDBOX local on linux", "linux", map[string]string{"AF_SANDBOX": "local"}, nil, "local", SandboxEnv},
		{"AF_SANDBOX local on darwin", "darwin", map[string]string{"AF_SANDBOX": "local"}, nil, "local", SandboxEnv},
		{"AF_SANDBOX local on windows", "windows", map[string]string{"AF_SANDBOX": "local"}, nil, "local", SandboxEnv},
		{"AF_SANDBOX podman on linux", "linux", map[string]string{"AF_SANDBOX": "podman"}, nil, "podman", SandboxEnv},
		{"AF_SANDBOX none on darwin", "darwin", map[string]string{"AF_SANDBOX": "none"}, nil, "none", SandboxEnv},
		{"--sandbox beats AF_SANDBOX", "linux", map[string]string{"AF_SANDBOX": "local"}, []string{"--sandbox", "none"}, "none", SandboxFlag},
		{"--sandbox beats the default", "linux", nil, []string{"--sandbox", "podman"}, "podman", SandboxFlag},
		{"an empty AF_SANDBOX is unset", "linux", map[string]string{"AF_SANDBOX": ""}, nil, "landlock", SandboxDefault},
		{"AF_SANDBOX that nothing reads", "linux", map[string]string{"AF_SANDBOX": "bogus"}, []string{"--sandbox", "local"}, "local", SandboxFlag},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, _ := recordingRegistry()

			s, err := resolve(t, reg, tc.goos, tc.env, tc.args...)

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
// all four providers, before any box.
// Verifies: 10-REQ-1.4
func TestAnInvalidAFSandboxIsUsageNamingTheVariable_TS_10_4(t *testing.T) {
	reg, made := recordingRegistry()

	_, err := resolve(t, reg, "linux", map[string]string{"AF_SANDBOX": "bogus"})

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
}

// TestBorrowedHandsGoesOnlyWithAgentkit_TS_10_7 verifies TS-10-7:
// --borrowed-hands with scripted or claude is refused as usage naming the flag and
// agentkit, with agentkit it is carried in the settings, and no box opens.
// Verifies: 10-REQ-1.7
func TestBorrowedHandsGoesOnlyWithAgentkit_TS_10_7(t *testing.T) {
	for _, args := range [][]string{
		{"--borrowed-hands", "--brain", "scripted", "--script", "x.json"},
		{"--borrowed-hands", "--brain", "claude"},
		{"--borrowed-hands", "--brain", "claude", "--sandbox", "local"},
	} {
		reg, made := recordingRegistry()

		_, err := resolve(t, reg, "linux", nil, args...)

		if err == nil {
			t.Errorf("%v was accepted", args)
			continue
		}
		usageNaming(t, err, "--borrowed-hands")
		if !strings.Contains(err.Error(), "agentkit") {
			t.Errorf("%v: the message %q does not name agentkit", args, err)
		}
		if n := made.Load(); n != 0 {
			t.Errorf("%v: %d providers were made", args, n)
		}
	}

	for _, args := range [][]string{
		{"--borrowed-hands"},
		{"--borrowed-hands", "--brain", "agentkit", "--sandbox", "landlock"},
		{"--borrowed-hands", "--sandbox", "local"}, // it changes nothing there, and is accepted
	} {
		reg, _ := recordingRegistry()
		s, err := resolve(t, reg, "linux", nil, args...)
		if err != nil || !s.BorrowedHands {
			t.Errorf("%v: BorrowedHands = %v, Validate = %v, want it accepted and carried", args, s.BorrowedHands, err)
		}
	}
	reg, _ := recordingRegistry()
	if s, err := resolve(t, reg, "linux", nil); err != nil || s.BorrowedHands {
		t.Errorf("without the flag: BorrowedHands = %v, Validate = %v", s.BorrowedHands, err)
	}
}

// TestACommandMayPinItsDefaultSandbox guards the default of a command that names its
// own: it is used before AF_SANDBOX and the default of the operating system, and
// --sandbox still wins over it.
func TestACommandMayPinItsDefaultSandbox(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{nil, "local"}, {[]string{"--sandbox", "none"}, "none"}} {
		reg, _ := recordingRegistry()
		old := box.GOOS
		box.GOOS = "linux"
		t.Cleanup(func() { box.GOOS = old })
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
