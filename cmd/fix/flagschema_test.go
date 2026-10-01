package main

import (
	"encoding/json"
	"flag"
	"io"
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// fixFlagsDoc builds fix's flags document from the same fully-populated
// FlagSet App.Main constructs: Common's flags, then the tool's own.
func fixFlagsDoc(t *testing.T) (props map[string]any, usage func(string) string) {
	t.Helper()
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var common toolio.Common
	common.Register(fs)
	newApp().Flags(fs)

	raw, err := json.Marshal(toolio.BuildFlagsDocument(fs))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc["properties"].(map[string]any), func(n string) string { return fs.Lookup(n).Usage }
}

// TS-09-19 (unit): fix's hand-written --pull flag, which is not a
// flag.Getter, is a plain string whose default is its own String().
func TestTS09_19_FixPullIsPlainString(t *testing.T) {
	props, usage := fixFlagsDoc(t)
	got := props["pull"].(map[string]any)
	want := map[string]any{
		"type":        "string",
		"default":     (&pullFlag{}).String(),
		"description": usage("pull"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pull = %v, want %v", got, want)
	}
}

// TS-09-20 (unit): fix's --land carries its declared enum; free-text --repo
// carries none.
func TestTS09_20_FixLandEnum(t *testing.T) {
	props, _ := fixFlagsDoc(t)
	if got := props["land"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"pr", "branch", "none"}) {
		t.Errorf("land enum = %v", got)
	}
	for _, name := range []string{"repo", "model", "dir"} {
		if _, ok := props[name].(map[string]any)["enum"]; ok {
			t.Errorf("--%s carries an enum", name)
		}
	}
	if _, ok := props["schema"]; ok {
		t.Error("schema flag present")
	}
	if _, ok := props["version"]; ok {
		t.Error("version flag present")
	}
	if d := props["verify-timeout"].(map[string]any); d["format"] != "duration" || d["default"] != "10m0s" {
		t.Errorf("verify-timeout = %v", d)
	}
}
