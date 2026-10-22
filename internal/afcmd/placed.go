package afcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// placedRun is a run directory as brain.Execute places it in a box before a brain
// runs: the brief, and a run.json with the run, the kind, the attempt, the nonce a
// submission has to carry and where the prefix of the brief ends. A process that is
// started in the box to run the brain finds nothing else of the host's run state
// there, and takes everything it needs from these two files.
type placedRun struct {
	Run    submission.Run
	Prefix string
	Task   string
	// Hook is hook.json, when the run directory has one.
	Hook *brain.HookConfig
}

// readPlaced reads the run directory of a process started inside the box, for an
// agent whose submissions are of the kind. It reports false, with no error, for a
// directory that is not one: no run.json, or one that does not say where the prefix
// ends. A run.json that says it and cannot be used (it is for another run or kind,
// has a nonce that is none, or a prefix that the brief does not have) is a usage
// failure: a submission made under it would not count.
func readPlaced(run *toolio.RunDir, kind string) (placedRun, bool, error) {
	data, err := os.ReadFile(filepath.Join(run.Path, "run.json"))
	if err != nil {
		return placedRun{}, false, nil
	}
	var placed placedRun
	if err := json.Unmarshal(data, &placed.Run); err != nil || placed.Run.PrefixBytes <= 0 {
		return placedRun{}, false, nil
	}
	bad := func(format string, args ...any) (placedRun, bool, error) {
		return placedRun{}, false, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s: run.json "+format, append([]any{run.ID}, args...)...)}
	}
	switch r := placed.Run; {
	case r.RunID != run.ID:
		return bad("is for run %q", r.RunID)
	case r.Kind != kind:
		return bad("is for the kind %q, and this is a %s run", r.Kind, kind)
	case r.Attempt < 1:
		return bad("has attempt %d, and attempts count from 1", r.Attempt)
	case !brain.ValidNonce(r.Nonce):
		return bad("has the nonce %q, which a submission could not be made under", r.Nonce)
	}
	document, err := os.ReadFile(filepath.Join(run.Path, "brief.md"))
	if err != nil {
		return bad("is there, and brief.md is not (%v)", err)
	}
	if n := placed.Run.PrefixBytes; n > len(document) || (n < len(document) && !utf8.RuneStart(document[n])) {
		return bad("says the prefix of the brief is %d bytes, and brief.md is %d bytes long, or cut inside a character", n, len(document))
	}
	placed.Prefix, placed.Task = string(document[:placed.Run.PrefixBytes]), string(document[placed.Run.PrefixBytes:])
	if hook, err := os.ReadFile(filepath.Join(run.Path, "hook.json")); err == nil {
		var h brain.HookConfig
		if json.Unmarshal(hook, &h) == nil {
			placed.Hook = &h
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return bad("is there, and hook.json cannot be read (%v)", err)
	}
	return placed, true, nil
}
