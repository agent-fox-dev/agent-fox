package codefix

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// noVerifyRepo is newRepo with nothing a verification command can be detected
// from: no Makefile, no manifest.
func noVerifyRepo(t *testing.T) (*Options, *scriptedBrain) {
	t.Helper()
	ws, g := newRepo(t, 0)
	gitOutFix(t, ws.Root, "rm", "-q", "Makefile")
	gitOutFix(t, ws.Root, "commit", "-q", "-m", "chore: no Makefile")
	b := defaultBrain()
	o := newOptions(ws, g, b)
	o.Runner = preflightRunner(t, 10, 2.0)
	return &o, b
}

// Issue #215: a repository the tool can detect no verification command for
// is refused before the model is called — an unverified change lands only
// when --no-verify asks for it — and the refusal says how to proceed.
func TestNoDetectableVerifyCommandIsRefusedBeforeTheModel(t *testing.T) {
	o, b := noVerifyRepo(t)
	res, err := Run(context.Background(), *o)
	var f *Failure
	if !errors.As(err, &f) || f.Category != "usage" || f.Stage != "preflight" {
		t.Fatalf("failure = %+v (%v), want a preflight usage refusal", f, err)
	}
	for _, want := range []string{"--verify", "--no-verify"} {
		if !strings.Contains(f.Error(), want) {
			t.Errorf("the refusal does not name %s: %s", want, f.Error())
		}
	}
	if b.analyzed != 0 || b.implemented != 0 || res.Commit != "" {
		t.Errorf("work was done: analyzed=%d implemented=%d commit=%q", b.analyzed, b.implemented, res.Commit)
	}

	// --preflight refuses the same way.
	if _, err := RunPreflight(context.Background(), *o); !errors.As(err, &f) || f.Category != "usage" {
		t.Errorf("RunPreflight: %v, want the same refusal", err)
	}

	// --no-verify is the explicit way to land unverified, and it is warned.
	o2, _ := noVerifyRepo(t)
	o2.NoVerify = true
	got, err := Run(context.Background(), *o2)
	if err != nil || got.Commit == "" {
		t.Fatalf("--no-verify: commit=%q err=%v", got.Commit, err)
	}
	warned := false
	for _, w := range o2.Run.Warnings() {
		warned = warned || w.Code == toolio.WarnNoVerifyCommand
	}
	if !warned {
		t.Error("--no-verify landed without a no_verify_command warning")
	}
}
