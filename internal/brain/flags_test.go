package brain

import (
	"flag"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// parse registers the shared flags with the defaults and parses the arguments.
func parse(t *testing.T, d Defaults, args ...string) *Settings {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Flags(fs, d)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return s
}

// countingProviders holds local and podman as entries whose provider is counted when
// it is made, which is what opening a box does.
func countingProviders() (*box.Registry, *atomic.Int32) {
	made := &atomic.Int32{}
	r := box.NewRegistry()
	for _, name := range []string{"local", "podman"} {
		r.Register(box.Entry{Name: name, Unconfined: name == "local", New: func() sandbox.Provider { made.Add(1); return local.Unisolated() }})
	}
	return r, made
}

func TestFlags_TakeTheDefaultsOfTheCommand(t *testing.T) {
	if s := parse(t, Defaults{}); s.Brain != AgentkitName || s.Sandbox != "" || s.Script != "" {
		t.Errorf("no defaults: %+v, want agentkit and the sandbox left to Validate", s)
	}
	s := parse(t, Defaults{Brain: ScriptedName, Sandbox: "local", Budget: 2.5, MaxTurns: 9}, "--sandbox", "podman", "--script", "s.json")
	if s.Brain != ScriptedName || s.Sandbox != "podman" || s.Budget != 2.5 || s.MaxTurns != 9 || s.Script != "s.json" {
		t.Errorf("settings = %+v, want the command's defaults and the flags that were given", s)
	}
}

func TestValidate_RefusesBoundsAndAScriptThatDoNotFit(t *testing.T) {
	d := Defaults{Budget: 5, MaxTurns: 40}
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"--budget", "0"}, "--budget"},
		{[]string{"--budget", "-0.01"}, "--budget"},
		{[]string{"--budget", "NaN"}, "--budget"},
		{[]string{"--budget", "+Inf"}, "--budget"},
		{[]string{"--max-turns", "0"}, "--max-turns"},
		{[]string{"--brain", "scripted"}, "--script"},
		{[]string{"--script", "x.json"}, "--script"},
		{[]string{"--brain", "other", "--script", "x.json"}, "--script"},
	} {
		f := asFailure(t, parse(t, d, tc.args...).Validate())
		if f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, tc.flag) {
			t.Errorf("%v: %+v, want a usage failure that names %s", tc.args, f, tc.flag)
		}
	}
	for _, args := range [][]string{nil, {"--budget", "0.01", "--max-turns", "1"}, {"--brain", "scripted", "--script", "x.json"}} {
		if err := parse(t, d, args...).ValidateIn(noEnv, box.Default); err != nil {
			t.Errorf("%v: Validate = %v, want nil", args, err)
		}
	}
}

func TestValidate_ChoosesTheSandboxByFlagThenAFSandboxThenPodman(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pinned  string
		env     map[string]string
		args    []string
		want    string
		source  string
		refuses []string // what the usage failure names, when it is one
	}{
		{"the default", "", nil, nil, "podman", SandboxDefault, nil},
		{"AF_SANDBOX", "", map[string]string{"AF_SANDBOX": "local"}, nil, "local", SandboxEnv, nil},
		{"an empty AF_SANDBOX is unset", "", map[string]string{"AF_SANDBOX": ""}, nil, "podman", SandboxDefault, nil},
		{"--sandbox over AF_SANDBOX", "", map[string]string{"AF_SANDBOX": "local"}, []string{"--sandbox", "podman"}, "podman", SandboxFlag, nil},
		{"the command's pin over AF_SANDBOX", "local", map[string]string{"AF_SANDBOX": "podman"}, nil, "local", SandboxFlag, nil},
		{"--sandbox over the command's pin", "local", nil, []string{"--sandbox", "podman"}, "podman", SandboxFlag, nil},
		{"a --sandbox that is no provider", "", nil, []string{"--sandbox", "docker"}, "", "", []string{"--sandbox", "docker", "local", "podman"}},
		{"an AF_SANDBOX that is no provider", "", map[string]string{"AF_SANDBOX": "bogus"}, nil, "", "", []string{"AF_SANDBOX", "bogus", "local, podman"}},
		{"an AF_SANDBOX that is no provider, with --sandbox", "", map[string]string{"AF_SANDBOX": "bogus"}, []string{"--sandbox", "local"}, "", "", []string{"AF_SANDBOX"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, made := countingProviders()
			s := parse(t, Defaults{Sandbox: tc.pinned, Budget: 5, MaxTurns: 40}, tc.args...)

			err := s.ValidateIn(envOf(tc.env), reg)

			if tc.refuses == nil && (err != nil || s.Sandbox != tc.want || s.SandboxSource != tc.source) {
				t.Errorf("Validate = %v; sandbox %q by %q, want %q by %q", err, s.Sandbox, s.SandboxSource, tc.want, tc.source)
			}
			if tc.refuses != nil {
				f := asFailure(t, err)
				for _, want := range tc.refuses {
					if f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, want) {
						t.Errorf("failure = %+v, want a usage failure that names %s", f, want)
					}
				}
			}
			if made.Load() != 0 {
				t.Errorf("%d providers were made while the flags were checked", made.Load())
			}
		})
	}
}
