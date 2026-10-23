package docs

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheDocumentsNameWhatTheLabelPollDoes_TS_15_63 verifies TS-15-63: each of the documents that the label poll changes
// names what the spec requires of it, in the words its surfaces use, the errata of the spec are there, and docs/cli.md,
// which the poll does not change, has no word of it.
// Verifies: 15-REQ-10.1
func TestTheDocumentsNameWhatTheLabelPollDoes_TS_15_63(t *testing.T) {
	api := read(t, "docs/api.md")
	if missing := lacking(sectionFrom(t, api, "### Jobs that a request label makes"),
		"`source` `{\"kind\": \"label\", \"by\": \"label af:fix\"}`", "`request_label`", "`af:fix`", "absorbed", "already taken", "held",
		"a label that the poll absorbed", "AF_POLL_INTERVAL",
	); len(missing) > 0 {
		t.Errorf("the section of docs/api.md on the jobs that a label makes does not contain %q", missing)
	}

	config := read(t, "docs/configuration.md")
	if missing := lacking(sectionFrom(t, config, "### The label poll"),
		"`AF_POLL_INTERVAL`", "`5m`", "`0`", "`1m`", "`24h`", "`label_prefix`", "`AF_LABEL_PREFIX`", "falls back to 5 minutes", "once for each distinct value",
	); len(missing) > 0 {
		t.Errorf("the section of docs/configuration.md on the label poll does not contain %q", missing)
	}

	if missing := lacking(read(t, "docs/model-usage.md"),
		"the label poll runs no model", "each labelled issue is a job that spends like a job of the API", "a restored label does not spend again",
	); len(missing) > 0 {
		t.Errorf("docs/model-usage.md does not contain %q", missing)
	}

	arch := read(t, "docs/architecture.md")
	if missing := lacking(sectionFrom(t, arch, "### The label poll, as built"),
		"`internal/service/poll`", "one loop for each subscribed workspace", "four cycles", "5 minutes", "owed removal", "absorbs", "hold", "release delay",
		"`af_poll_requests`", "outlive", "incomplete", "requests of hub", "forge", "`Standing`",
	); len(missing) > 0 {
		t.Errorf("the section of docs/architecture.md on the label poll does not contain %q", missing)
	}

	if missing := lacking(read(t, "docs/development.md"),
		"`internal/service/poll`", "`SetAdditionLag`", "`SetRemovalLag`", "`SetPageLinks`",
	); len(missing) > 0 {
		t.Errorf("docs/development.md does not contain %q", missing)
	}

	if missing := lacking(read(t, "README.md"), "picks up", "`AF_POLL_INTERVAL`"); len(missing) > 0 {
		t.Errorf("README.md does not contain %q", missing)
	}

	if cli := strings.ToLower(read(t, "docs/cli.md")); strings.Contains(cli, "poll") {
		t.Error("docs/cli.md speaks of the poll, which has no command or flag")
	}

	if files, err := filepath.Glob(filepath.Join("errata", "15_*.md")); err != nil || len(files) == 0 {
		t.Errorf("there is no docs/errata/15_*.md: %v", err)
	}
}
