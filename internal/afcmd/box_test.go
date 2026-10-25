package afcmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// gateway makes the box of the environment the one of a fake hub's gateway, whose boxes find
// the af of afBin first on their PATH, and returns the gateway that records what they ran.
func (e *env) gateway() *fakehub.Gateway {
	e.t.Helper()
	t := e.t
	t.Setenv("PATH", afBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := fakehub.New(t)
	h.AddWorkspace(t, fakehub.Workspace{Slug: "acme", PAT: "pat-of-the-box-tests"})
	lease, err := hub.New(h.URL, "1").Lease(context.Background(), "pat-of-the-box-tests", "acme", hub.LeaseRequest{Purpose: "fix"})
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
	e.cfg.Boxes = box.NewRegistry()
	e.cfg.Boxes.Register(entry)
	e.cfg.SandboxProfile = "py"
	return h.Gateway()
}

// measured are the commands of af that measure the tree, as the box ran them, in order.
func measured(g *fakehub.Gateway) []string {
	var out []string
	for _, c := range g.Commands() {
		for _, name := range []string{"--version", "lang detect", "gate run", "revert-check"} {
			if strings.Contains(c.Command, "'af' '"+strings.ReplaceAll(name, " ", "' '")+"'") {
				out = append(out, name)
			}
		}
	}
	return out
}

func TestFix_InTheBoxOfAGatewayMeasuresThereAndLandsOnTheHost(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	g := e.gateway()
	e.cfg.BoxLabels = map[string]string{"af.job": "job_1"}

	o := e.fix(e.forge.IssueURL(7), append(scripted(t, fixSteps()...), "--sandbox", "gateway")...)

	if o.code != 0 || o.res()["outcome"] != "landed" || o.warned("box_af_version_mismatch") {
		t.Fatalf("%v; want a landed change, and no warning of the af of the box, which is this one", o)
	}
	if got, want := measured(g), []string{"--version", "lang detect", "gate run", "gate run", "revert-check"}; !slices.Equal(got, want) {
		t.Errorf("the box ran %v, want %v", got, want)
	}
	created := g.Created()
	if len(created) != 1 || created[0].Spec.Labels["af.tool"] != "fix" || created[0].Spec.Labels["af.job"] != "job_1" {
		t.Errorf("boxes %+v, want one for the attempt, labelled", created)
	}
	after, _ := o.res()["after"].(map[string]any)
	if id, _ := after["gate_id"].(string); id == "" || testutil.ReadFile(t, filepath.Join(e.dir, ".agent-fox", "gate", id, "result.json")) == "" {
		t.Errorf("the result of the gate after the change is not on the host: %v", after)
	}
}

func TestFix_AnAfOfAnotherVersionInTheBoxIsWarnedOfAndTheRunGoesOn(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	e.gateway()
	e.cfg.Version = "v-other"

	o := e.fix(e.forge.IssueURL(7), append(scripted(t, fixSteps()...), "--sandbox", "gateway")...)

	if o.code != 0 || !o.warned("box_af_version_mismatch") || o.res()["outcome"] != "landed" {
		t.Errorf("%v; want the warning box_af_version_mismatch and a landed change", o)
	}
}

func TestFix_ABoxWithoutAUsableAfFailsForGoodNamingItsProfile(t *testing.T) {
	for profile, named := range map[string]string{"py": "py", "": "default"} {
		t.Run(named, func(t *testing.T) {
			e := newEnv(t, repoFiles(), true)
			g := e.gateway()
			e.cfg.SandboxProfile = profile
			broken := testutil.WriteFile(t, t.TempDir(), "af", "#!/bin/sh\necho broken >&2; exit 3\n")
			if err := os.Chmod(broken, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(broken)+string(os.PathListSeparator)+os.Getenv("PATH"))

			o := e.fix(e.forge.IssueURL(7), append(scripted(t, fixSteps()...), "--sandbox", "gateway")...)

			if o.code == 0 || o.category() != "sandbox" || o.Error.Retryable || !strings.Contains(o.message(), named) || !strings.Contains(o.message(), "image must provide af") {
				t.Errorf("%v; want a sandbox failure, not retryable, that names the profile %s", o, named)
			}
			if got := measured(g); slices.ContainsFunc(got, func(c string) bool { return c != "--version" }) {
				t.Errorf("the box ran %v of the checks of the repository", got)
			}
		})
	}
}
