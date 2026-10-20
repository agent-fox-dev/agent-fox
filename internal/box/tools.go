package box

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/agent-fox-dev/agentkit-go/core"
)

// The names of the hands' tools that the session itself uses, as the host's
// own hands and not as a brain's: they are not asked of the gate.
const (
	toolExecute = "execute"
)

// call runs one of the box's tools with the arguments and returns its result.
func (s *Session) call(ctx context.Context, name string, args map[string]any) (core.ToolResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return core.ToolResult{}, err
	}
	for _, t := range s.tools {
		if t.Name == name {
			return t.Execute(ctx, raw), nil
		}
	}
	return core.ToolResult{}, fmt.Errorf("the box has no %s tool", name)
}
