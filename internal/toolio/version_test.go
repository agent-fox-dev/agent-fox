package toolio

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"
)

func TestVersion_IsWhatTheBuildSetElseItsBuildInformationElseDev(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "deadbeef"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	}
	for _, tc := range []struct {
		name string
		app  *App
		want [3]string // version, commit, build time
	}{
		{"set by the build", &App{Version: "v1.2.3", Commit: "abc123", BuildTime: "2026-10-20T00:00:00Z", BuildInfo: func() (*debug.BuildInfo, bool) { return info, true }},
			[3]string{"v1.2.3", "abc123", "2026-10-20T00:00:00Z"}},
		{"partly set by the build", &App{Version: "v1.2.3", BuildInfo: func() (*debug.BuildInfo, bool) { return info, true }},
			[3]string{"v1.2.3", "deadbeef", "2026-01-02T03:04:05Z"}},
		{"the build information", &App{BuildInfo: func() (*debug.BuildInfo, bool) { return info, true }},
			[3]string{"v9.9.9", "deadbeef", "2026-01-02T03:04:05Z"}},
		{"a development build", &App{BuildInfo: func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
		}}, [3]string{"dev", "", ""}},
		{"no build information", &App{BuildInfo: func() (*debug.BuildInfo, bool) { return nil, false }}, [3]string{"dev", "", ""}},
	} {
		r := run(t, tc.app, "--version")
		want := map[string]any{"name": "af", "version": tc.want[0], "commit": tc.want[1], "build_time": tc.want[2], "go_version": runtime.Version()}
		if r.code != 0 || r.stderr != "" || !reflect.DeepEqual(r.env(t), want) {
			t.Errorf("%s: exit %d, stderr %q, document %s; want %v", tc.name, r.code, r.stderr, r.stdout, want)
		}
		if keys := topLevelKeys(t, r.stdout); !slices.Equal(keys, []string{"name", "version", "commit", "build_time", "go_version"}) {
			t.Errorf("%s: keys %v", tc.name, keys)
		}
		version, commit := tc.app.Identity()
		if doc := tc.app.VersionDocument(); version != tc.want[0] || commit != tc.want[1] || doc.Version != version || doc.BuildTime != tc.want[2] {
			t.Errorf("%s: Identity %q, %q and VersionDocument %+v; want what --version prints", tc.name, version, commit, doc)
		}
	}
}
