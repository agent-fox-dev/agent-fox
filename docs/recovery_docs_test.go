package docs

import (
	"path/filepath"
	"regexp"
	"testing"
)

// TestTheDocumentsNameTheRecoveryTermsTheSpecRequires_TS_14_59 verifies TS-14-59: each of the documents that the
// recovery of attempts changes names what the spec requires of it, in the words the surfaces use, and a file of errata
// records the divergences of the spec.
// Verifies: 14-REQ-10.1
func TestTheDocumentsNameTheRecoveryTermsTheSpecRequires_TS_14_59(t *testing.T) {
	arch := read(t, "docs/architecture.md")
	if missing := lacking(sectionFrom(t, arch, "### Recovery, as built"),
		"`recovery/`", "`journal.json`", "`outcome.json`", "mode 0600", "the handover count", "pinned", "`resume_discarded`",
		"stage `outcome`", "stage `published`", "stage `applied`", "stage `submitted`", "stage `brain`",
		"re-measures", "runs the baseline again", "set aside", "box is replaced", "`box_replaced`", "`lease_shortened_timeout`",
		"reconciliation pass", "orphan", "`Agent-Fox-Run`", "`attempt_resumed`", "stage-abort",
	); len(missing) > 0 {
		t.Errorf("the section on recovery in docs/architecture.md does not contain %q", missing)
	}

	api := read(t, "docs/api.md")
	if missing := lacking(api,
		"`attempt_resumed`", "`resume_discarded`", "`box_replaced`", "`journal_unwritable`", "`brain_spend_unrecorded`",
		"A `deliveries` above 1 means a resume",
	); len(missing) > 0 {
		t.Errorf("docs/api.md does not contain %q", missing)
	}

	cli := read(t, "docs/cli.md")
	if !regexp.MustCompile(`brain_end \{[^}]*input_tokens[^}]*output_tokens[^}]*\}`).MatchString(cli) {
		t.Error("docs/cli.md does not give input_tokens and output_tokens among the fields of brain_end")
	}

	if missing := lacking(read(t, "docs/model-usage.md"),
		"never runs the brain again for a submission that the host accepted and that is still good", "what is left of the budget", "the usage of the whole attempt",
		"`brain_spend_unrecorded`",
	); len(missing) > 0 {
		t.Errorf("docs/model-usage.md does not contain %q", missing)
	}

	if missing := lacking(read(t, "docs/development.md"),
		"`SeedLease`", "`KillSandbox`", "lists the leases", "the stage-abort seam", "`Runner.abortAfter`", "`recovery/`",
	); len(missing) > 0 {
		t.Errorf("docs/development.md does not contain %q", missing)
	}

	if files, err := filepath.Glob(filepath.Join("errata", "14_*.md")); err != nil || len(files) == 0 {
		t.Errorf("there is no docs/errata/14_*.md: %v", err)
	}
}
