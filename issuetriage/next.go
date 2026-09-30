package issuetriage

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Next implements toolio.NextProvider (06-REQ-6.1): when an issue was
// actually filed or updated, suggest fixing it, by URL. Everything it reads
// — Action, URL, Labels, DryRun — is a fact Write established; nothing comes
// from the diagnosis the model wrote. Nothing is suggested under --dry-run,
// where there is no URL yet, or when no issue was written.
func (r *Result) Next() []toolio.Next {
	if r == nil || r.DryRun || r.Action == "none" || r.Action == "" || r.URL == "" {
		return nil
	}
	why := "the issue was filed"
	if r.Action == "updated" {
		why = "the issue was updated"
	}
	if len(r.Labels) > 0 {
		why += " and labelled " + strings.Join(r.Labels, ", ")
	}
	why += fmt.Sprintf("; fix can work from %s", r.URL)
	return []toolio.Next{{Tool: "fix", Input: r.URL, Flags: []string{}, Why: why}}
}
