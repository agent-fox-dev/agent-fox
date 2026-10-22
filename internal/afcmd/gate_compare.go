package afcmd

import (
	"flag"
	"fmt"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerGateCompare registers af gate compare.
func registerGateCompare(app *toolio.App) {
	var before, after string
	app.Register(toolio.Command{
		Path:    "gate compare",
		Summary: "Compare two stored gate results, check by check",
		Usage:   "af gate compare --before LABEL|ID --after LABEL|ID [--dir DIR]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&before, "before", "", "the result to compare from: a `label` or a gate id")
			fs.StringVar(&after, "after", "", "the result to compare to: a `label` or a gate id")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 30 * time.Second,
		ResultSchema:   resultSchema("gate_compare"),
		Run: func(r *toolio.Run) (toolio.Outcome, error) {
			c, err := checks.Compare(r.Dir, before, after)
			if err != nil {
				return toolio.Outcome{}, checksFailure(r, err)
			}
			landable := "not landable"
			if c.Landable {
				landable = "landable"
			}
			return toolio.Outcome{
				Status:  toolio.StatusDone,
				Summary: fmt.Sprintf("%s (%s): %s -> %s", c.Verdict, landable, sideName(c.Before), sideName(c.After)),
				Result:  c,
			}, nil
		},
	})
}

// sideName is how a comparison names a result: by its label if it has one.
func sideName(s checks.Side) string {
	if s.Label != "" {
		return s.Label
	}
	return s.ID
}
