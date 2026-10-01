package main

import (
	"encoding/json"
	"flag"
	"io"
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-09-20 (unit): impl's --land carries its declared enum.
func TestTS09_20_ImplLandEnum(t *testing.T) {
	fs := flag.NewFlagSet("impl", flag.ContinueOnError)
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
	props := doc["properties"].(map[string]any)
	if got := props["land"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"pr", "branch", "none"}) {
		t.Errorf("land enum = %v", got)
	}
	if _, ok := props["repo"].(map[string]any)["enum"]; ok {
		t.Error("--repo carries an enum")
	}
}
