package afcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestTheLoopsTakeTheirOptionsFromACall pins the options of a call of af triage and af fix:
// LoopOptions, over the defaults of the flags, and a usage failure for another type or a landing
// that is not one.
func TestTheLoopsTakeTheirOptionsFromACall(t *testing.T) {
	s := brain.Settings{Brain: "agentkit", Budget: 10, MaxTurns: 150}
	LoopOptions{Brain: "scripted", MaxTurns: 3, Script: "s.json"}.apply(&s)
	if s.Brain != "scripted" || s.Budget != 10 || s.MaxTurns != 3 || s.Script != "s.json" || s.Sandbox != "" {
		t.Errorf("settings %+v, want the options that are set over the defaults", s)
	}

	dir := t.TempDir()
	for name, c := range map[string]toolio.Call{
		"fix, another type":      {Path: "fix", Args: []string{"https://github.com/o/r/issues/1"}, Dir: dir, Options: "pr"},
		"fix, no landing":        {Path: "fix", Args: []string{"https://github.com/o/r/issues/1"}, Dir: dir, Options: LoopOptions{Land: "somewhere"}},
		"triage, another type":   {Path: "triage", Args: []string{"-"}, Dir: dir, Options: &LoopOptions{}},
		"triage run, no options": {Path: "triage run", Dir: dir, Options: LoopOptions{}},
	} {
		t.Run(name, func(t *testing.T) {
			env, encoded := newApp().Invoke(context.Background(), c)

			if env.Status != toolio.StatusUsage || !strings.Contains(env.Error.Message, c.Path) && !strings.Contains(env.Error.Message, "somewhere") {
				t.Errorf("envelope %s, want a usage failure", encoded)
			}
		})
	}
}
