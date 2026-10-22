package docs

import (
	"fmt"
	"regexp"
	"testing"
)

// TestTheDocumentsNameTheJobsTermsTheSpecRequires_TS_12_8 verifies TS-12-8: each of the
// documents that the jobs layer changes names what the spec requires of it, in the words
// the surfaces use, and does not leave the sentences that said the jobs did not exist.
// Verifies: 12-REQ-1.8
func TestTheDocumentsNameTheJobsTermsTheSpecRequires_TS_12_8(t *testing.T) {
	api := read(t, "docs/api.md")
	if missing := lacking(api,
		// the routes
		"POST /api/v1/workspaces/{slug}/triage", "POST /api/v1/workspaces/{slug}/fix", "GET /api/v1/workspaces/{slug}/jobs", "GET /api/v1/jobs",
		"GET /api/v1/jobs/{id}", "POST /api/v1/jobs/{id}/cancel", "POST /api/v1/jobs/{id}/answer", "POST /api/v1/jobs/{id}/retry",
		"GET /api/v1/jobs/{id}/attempts/{n}/envelope", "GET /api/v1/jobs/{id}/attempts/{n}/report", "GET /api/v1/jobs/{id}/events",
		// the body limits
		"1 MiB", "256 KiB", "64 KiB",
		// the documents
		"### The job document", "`attempt_count`", "`current_attempt`", "`finished_at`", "`labels`", "`usage`", "`hub`", "`blocked`", "`attempts`",
		"`trigger`", "`deliveries`", "`transient_failures`", "`job_state`", "`attempt_start`", "`attempt_end`", "`invalid_event_type`",
		// reading
		"`next_cursor`", "`limit`", "`cursor`", "text/markdown; charset=utf-8",
		// the stream
		"text/event-stream", "`Last-Event-ID`", "`since`", "`at_rest`", ": keepalive", "256 events", "id: 7", "event: attempt_end", "data: {",
	); len(missing) > 0 {
		t.Errorf("docs/api.md does not contain %q", missing)
	}
	for code, status := range map[string]int{"invalid_state": 409, "duplicate_job": 409, "not_subscribed": 409, "token_unreadable": 409} {
		row := regexp.MustCompile(fmt.Sprintf("\\|\\s*`%s`\\s*\\|\\s*%d\\s*\\|", code, status))
		if !row.MatchString(api) {
			t.Errorf("docs/api.md has no table row giving %s the status %d", code, status)
		}
	}
	if missing := lacking(api, "queued", "running", "blocked", "done", "failed", "cancelled"); len(missing) > 0 {
		t.Errorf("docs/api.md does not name the states %q", missing)
	}
	for _, stale := range []string{"there are no jobs yet to cancel", "Jobs, the worker, the poll and the\n`af` client come in later specs"} {
		if lacking(api, stale) == nil {
			t.Errorf("docs/api.md still says %q", stale)
		}
	}

	if missing := lacking(read(t, "docs/configuration.md"),
		"`workers.count`", "`database.retain`", "`[workers]`", "`[database]`", "720h", "`finished_at`", "`blocked` job is never removed",
	); len(missing) > 0 {
		t.Errorf("docs/configuration.md does not contain %q", missing)
	}

	if missing := lacking(read(t, "docs/model-usage.md"),
		"Automatic attempts spend more", "`brain`", "`no_result`", "sum of `cost_usd`", "`usage`",
	); len(missing) > 0 {
		t.Errorf("docs/model-usage.md does not contain %q", missing)
	}

	arch := read(t, "docs/architecture.md")
	if missing := lacking(sectionFrom(t, arch, "### The jobs, as built"),
		"`af_jobs`", "`af_attempts`", "`af_events`", "`queued`", "`running`", "`blocked`", "`done`", "`failed`", "`cancelled`", "`needs_human`",
		"**The queue key and the order.**", "`<job id>:<n>`", "nonce", "`af_attempts.queue_seq`", "`[workers].count`",
		"**The runner contract**", "Four obligations", "`jobs.Transient`", "`jobs.IsCancel`", "`ErrShutdown`",
		"**Transient retries versus automatic attempts.**", "`brain` or `no_result`", "**The event log.**", "`seq`", "**Retention.**",
	); len(missing) > 0 {
		t.Errorf("the section on the jobs in docs/architecture.md does not contain %q", missing)
	}

	if missing := lacking(read(t, "docs/development.md"), "`internal/service/jobs`", "`internal/service/jobs/fakerunner`", "`internal/service/jobs/fakeclock`"); len(missing) > 0 {
		t.Errorf("docs/development.md does not contain %q", missing)
	}
	if missing := lacking(read(t, "README.md"), "`jobs`"); len(missing) > 0 {
		t.Errorf("README.md does not contain %q", missing)
	}
}
