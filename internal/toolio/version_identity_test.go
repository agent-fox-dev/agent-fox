package toolio

import (
	"runtime/debug"
	"testing"
)

// TestIdentityIsTheVersionAndCommitThatVersionPrints guards the accessor commands use to
// tell their own af from another: what the build set, else what its build information has.
func TestIdentityIsTheVersionAndCommitThatVersionPrints(t *testing.T) {
	set := &App{Version: "v1.2.3", Commit: "abc123", BuildTime: "t"}
	if v, c := set.Identity(); v != "v1.2.3" || c != "abc123" {
		t.Errorf("Identity = %q, %q, want what the build set", v, c)
	}
	fromInfo := &App{BuildInfo: func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v0.9.0"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}}}, true
	}}
	if v, c := fromInfo.Identity(); v != "v0.9.0" || c != "deadbeef" {
		t.Errorf("Identity = %q, %q, want the version and revision of the build information", v, c)
	}
	if v, c := (&App{BuildInfo: func() (*debug.BuildInfo, bool) { return nil, false }}).Identity(); v != "dev" || c != "" {
		t.Errorf("Identity = %q, %q, want dev and no commit", v, c)
	}
}
