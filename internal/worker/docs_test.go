package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/catalog"
)

// TestTheDocumentationDescribesTheWorker_TS_13_81 verifies the part of TS-13-81 that can be
// checked without running the whole gate: each document holds what 13-REQ-10.8 names for it, and
// docs/cli.md is as it is on main, but for the token fields of brain_end that a later spec added. `make check` runs the docs tests of the other packages.
// Verifies: 13-REQ-10.8
func TestTheDocumentationDescribesTheWorker_TS_13_81(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	model := catalog.Default().DefaultModelID("anthropic")

	for doc, phrases := range map[string][]string{
		"docs/architecture.md": {
			"The worker", "transient error", "gateway box", "must provide `af`", "measured in the box", "label mirror",
			"in-progress", "AF_LABEL_PREFIX", "label_prefix_ignored", "hub_session", "PostPush",
		},
		"docs/configuration.md": {
			"AF_BRAIN_TRIAGE", "AF_BRAIN_FIX", "AF_BRAIN", "AF_MODEL", "AF_MAX_BUDGET_USD", "AF_MAX_TURNS", "AF_TIMEOUT", "AF_MAX_ATTEMPTS",
			"AF_SANDBOX_PROFILE", "AF_FIX_LAND", "AF_LABEL_PREFIX", "`15m`", "`45m`", "24h", "carry_patch",
		},
		"docs/model-usage.md": {"AF_MODEL", model, "vendor key", "profile", "AF_MAX_BUDGET_USD", "scripted"},
		"docs/api.md": {
			"hub_session", "sandbox", "`hub`", "`unsupported`", "carry_patch", "cannot be retrieved",
			"lease_shortened_timeout", "lease_release_failed", "label_prefix_ignored", "status_label_failed", "comment_failed",
			"request_label_failed", "hub_usage_failed", "hub_events_failed", "hub_session_failed",
		},
		"docs/development.md": {"internal/afcmd", "internal/worker", "cmd/af"},
		"README.md":           {"runs triage and fix attempts", "internal/worker"},
	} {
		text := read(doc)
		for _, phrase := range phrases {
			if !strings.Contains(text, phrase) {
				t.Errorf("%s does not hold %q", doc, phrase)
			}
		}
	}
}
