package indextest

import (
	"encoding/json"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// ToolTurn is a scripted model turn that calls one tool.
func ToolTurn(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse,
	}
}

// Offered is, for each request the scripted model received, the names of the
// tools it was offered — what reached the wire, not what a phase asked for.
func Offered(p *faux.Provider) [][]string {
	var out [][]string
	for _, req := range p.Requests() {
		var names []string
		for _, tl := range req.Tools {
			names = append(names, tl.Name)
		}
		out = append(out, names)
	}
	return out
}

// Has reports whether names contains want.
func Has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// HasWarning reports whether an envelope carries a warning with this code and
// severity.
func HasWarning(env map[string]any, code, severity string) bool {
	ws, _ := env["warnings"].([]any)
	for _, w := range ws {
		m, _ := w.(map[string]any)
		if m["code"] == code && m["severity"] == severity {
			return true
		}
	}
	return false
}
