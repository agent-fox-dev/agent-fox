package toolio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// reportTimestampLayout is a colon-free variant of RFC3339. RFC3339 contains
// ':', which is legal in a POSIX filename but awkward to tab-complete and
// glob; the envelope's own started_at field is unaffected, since it keeps
// the ordinary RFC3339 rendering.
const reportTimestampLayout = "20060102T150405Z"

// DefaultReportPath computes where a run's report file is written when
// --report-file is not given:
// $XDG_STATE_HOME/agent-fox/runs/<tool>-<started_at, colon-free>-<pid>.json,
// falling back to ~/.local/state/agent-fox/runs/... when $XDG_STATE_HOME is
// unset. It returns an error when neither $XDG_STATE_HOME nor $HOME (nor its
// platform equivalent) can be resolved, so a caller can record a warning
// rather than write nowhere silently.
func DefaultReportPath(tool string, startedAt time.Time, pid int) (string, error) {
	base, err := stateHome()
	if err != nil {
		return "", err
	}
	ts := startedAt.UTC().Format(reportTimestampLayout)
	name := fmt.Sprintf("%s-%s-%d.json", tool, ts, pid)
	return filepath.Join(base, "agent-fox", "runs", name), nil
}

// stateHome resolves the XDG state directory: $XDG_STATE_HOME if set, else
// ~/.local/state.
func stateHome() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return xdg, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("neither $XDG_STATE_HOME nor a home directory could be resolved: %w", err)
	}
	return filepath.Join(home, ".local", "state"), nil
}

// WriteReport writes env to path, creating the parent directory as needed.
// The bytes are exactly what Emit writes to stdout for the same envelope —
// json.MarshalIndent with the same prefix/indent, plus a trailing newline —
// so a report file, once read back, is byte-for-byte what --detail full
// would have printed for the same run (06-REQ-2.8).
func WriteReport(path string, env Envelope) error {
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
