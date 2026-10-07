package toolio

// WarnCode identifies one warning cause. Every declared constant has exactly
// one stage recorded in warnStages below, so a code cannot be used without a
// stage, and cannot be recorded against two different stages by accident.
type WarnCode string

// Declared warning codes. This is not exhaustive by design (05-REQ-5.6): a
// call site that fits none of these gets a new constant added here and to
// warnStages, never a bare string.
const (
	WarnInputTruncated     WarnCode = "input_truncated"
	WarnCommentsUnreadable WarnCode = "comments_unreadable"
	// WarnCommentsTruncated is recorded when an issue has more comments than
	// were read: the forge client stops after a fixed number of pages. It is
	// not the input byte bound, which is input_truncated.
	WarnCommentsTruncated      WarnCode = "comments_truncated"
	WarnNoVerifyCommand        WarnCode = "no_verify_command"
	WarnCriteriaUnmet          WarnCode = "criteria_unmet"
	WarnCommitNotParked        WarnCode = "commit_not_parked"
	WarnCheckoutNotRestored    WarnCode = "checkout_not_restored"
	WarnPullRequestNotOpened   WarnCode = "pull_request_not_opened"
	WarnCommentNotPosted       WarnCode = "comment_not_posted"
	WarnSpecEditReverted       WarnCode = "spec_edit_reverted"
	WarnStateNotSaved          WarnCode = "state_not_saved"
	WarnGateEdited             WarnCode = "gate_edited"
	WarnDocsNotUpdated         WarnCode = "docs_not_updated"
	WarnToolErrors             WarnCode = "tool_errors"
	WarnScratchFileRemoved     WarnCode = "scratch_file_removed"
	WarnScratchFileSuspected   WarnCode = "scratch_file_suspected"
	WarnDraftPackage           WarnCode = "draft_package"
	WarnUpstreamMissing        WarnCode = "upstream_missing"
	WarnParkedAttemptDiscarded WarnCode = "parked_attempt_discarded"
	WarnSpecValidationWarning  WarnCode = "spec_validation_warning"
	WarnSpecsDirUnreadable     WarnCode = "specs_dir_unreadable"
	WarnProjectLanguageUnknown WarnCode = "project_language_unknown"
	WarnNameFlagIgnored        WarnCode = "name_flag_ignored"
	WarnScopeRenamed           WarnCode = "scope_renamed"
	WarnScopeCountMismatch     WarnCode = "scope_count_mismatch"
	WarnSplitPlanForeign       WarnCode = "split_plan_foreign"
	WarnSplitPlanUnreadable    WarnCode = "split_plan_unreadable"
	WarnSplitPlanStale         WarnCode = "split_plan_stale"
	WarnSplitPlanUpdateFailed  WarnCode = "split_plan_update_failed"
	WarnSplitPlanNotRemoved    WarnCode = "split_plan_not_removed"
	WarnArchitectureNotWritten WarnCode = "architecture_not_written"
	WarnActivationFailed       WarnCode = "activation_failed"
	// WarnIssueBodyTruncated is recorded when triage cut the issue body to
	// fit the forge's body limit before writing it.
	WarnIssueBodyTruncated WarnCode = "issue_body_truncated"
	WarnRejectedPathCalls  WarnCode = "rejected_path_calls"
	// WarnReportFileNotWritten is recorded (06-REQ-2.3) when the report
	// file could not be written — a permission error, a read-only
	// filesystem, or a path that could not be computed. The run still
	// succeeds or fails on its own merits; only the envelope's report_file
	// field is affected, and it is omitted.
	WarnReportFileNotWritten WarnCode = "report_file_not_written"
	// WarnInputLooksLikePath is recorded (06-REQ-7.1) when Resolve classified
	// the argument as text, but it is a single whitespace-free line shaped
	// like a path and nothing exists there: a plausible typo that would
	// otherwise silently become prose.
	WarnInputLooksLikePath WarnCode = "input_looks_like_path"
	// WarnOutputNotWritten is recorded (08-REQ-3.1) when the second copy of
	// the envelope requested with --output could not be written. It never
	// changes the run's exit code or ok.
	WarnOutputNotWritten WarnCode = "output_not_written"
	// WarnOutputMatchesReportFile is recorded (08-REQ-4.4) when --output and
	// the report file name the same configured destination: --output's own
	// write is skipped and the file there, when the report write succeeds,
	// holds the complete report rather than the --detail view.
	WarnOutputMatchesReportFile WarnCode = "output_matches_report_file"
	// WarnEventsFileNotWritten is recorded (12-REQ-4.6) when the events
	// file could not be created — a permission error, a read-only
	// filesystem, a path that could not be computed, or a collision. The
	// run still succeeds or fails on its own merits; only the events file
	// is affected.
	WarnEventsFileNotWritten WarnCode = "events_file_not_written"
	// WarnEffortClamped is recorded (13-REQ-2.2) when the requested
	// reasoning effort was clamped to a different level by
	// catalog.ClampThinkingLevel. The warning message names both levels
	// and the model.
	WarnEffortClamped WarnCode = "effort_clamped"
	// WarnReviewNotRun is recorded when the independent conformance review
	// did not complete: nothing but the authors checked the change against
	// the spec, and the result says so instead of passing for reviewed.
	WarnReviewNotRun WarnCode = "review_not_run"
	// WarnResolveNotRun is recorded when the conformance stage found
	// something to fix and the resolve phase was not run, so nothing tried to
	// fix or declare it before the run reported it.
	WarnResolveNotRun WarnCode = "resolve_not_run"
	// WarnUnmetRequirements is recorded when the change knowingly does not
	// meet part of its specification. The pull request opens with the list.
	WarnUnmetRequirements WarnCode = "unmet_requirements"
	// WarnDeviationNotTracked is recorded for a declared deviation that no
	// erratum in the change records and no issue could be filed for.
	WarnDeviationNotTracked WarnCode = "deviation_not_tracked"
	// WarnFixNotProven is recorded when the checks still pass with a fix's
	// implementation taken out: no test depends on the fix, so the issue is
	// referenced rather than closed.
	WarnFixNotProven WarnCode = "fix_not_proven"
	// WarnRelevantFilesUnavailable is recorded when a resumed split skips
	// the PRD phase and no relevant_files list is available for the later
	// generation phases.
	WarnRelevantFilesUnavailable WarnCode = "relevant_files_unavailable"
	// WarnRepoMapBuildFailed is recorded (14-REQ-10.1) when building the
	// repository map failed. The phase runs without a map, as it did before
	// the map existed; navigation is an optimisation and never fails a run.
	WarnRepoMapBuildFailed WarnCode = "repo_map_build_failed"

	// WarnCodeSearchUnavailable: the code-search index could not be built
	// (unsupported platform or build failure). The run continues without
	// code_search; search_files remains available (16-REQ-5).
	WarnCodeSearchUnavailable WarnCode = "code_search_unavailable"

	// WarnUntrackedFilesLeftAlone is recorded once for each untracked file a
	// commit left out because the run did not create it: it was in the tree
	// before the phase started, or it is under the specs directory outside
	// the run's own spec package. The file stays where it is.
	WarnUntrackedFilesLeftAlone WarnCode = "untracked_files_left_alone"
	// WarnUnlistedFileCommitted is recorded for a new file a commit carries
	// that the phase's report does not list among its changes. It is
	// committed — the gate ran with it — and named so a reviewer can check
	// that it is the phase's work.
	WarnUnlistedFileCommitted WarnCode = "unlisted_file_committed"
)

// warnStages is the single table mapping every declared WarnCode to the
// pipeline stage that raises it. This is what Run.Warn looks Stage up from,
// rather than accepting it as a call-site parameter, so a code can never be
// recorded against two different stages.
var warnStages = map[WarnCode]string{
	WarnInputTruncated:           "input",
	WarnCommentsUnreadable:       "input",
	WarnCommentsTruncated:        "input",
	WarnNoVerifyCommand:          "preflight",
	WarnCriteriaUnmet:            "implement",
	WarnCommitNotParked:          "park",
	WarnCheckoutNotRestored:      "park",
	WarnPullRequestNotOpened:     "land",
	WarnCommentNotPosted:         "report",
	WarnSpecEditReverted:         "task",
	WarnStateNotSaved:            "park",
	WarnGateEdited:               "task",
	WarnDocsNotUpdated:           "task",
	WarnToolErrors:               "phase",
	WarnScratchFileRemoved:       "task",
	WarnScratchFileSuspected:     "task",
	WarnDraftPackage:             "preflight",
	WarnUpstreamMissing:          "preflight",
	WarnParkedAttemptDiscarded:   "preflight",
	WarnSpecValidationWarning:    "preflight",
	WarnSpecsDirUnreadable:       "preflight",
	WarnProjectLanguageUnknown:   "preflight",
	WarnNameFlagIgnored:          "usage",
	WarnScopeRenamed:             "prd",
	WarnScopeCountMismatch:       "prd",
	WarnSplitPlanForeign:         "split",
	WarnSplitPlanUnreadable:      "split",
	WarnSplitPlanStale:           "split",
	WarnSplitPlanUpdateFailed:    "split",
	WarnSplitPlanNotRemoved:      "split",
	WarnArchitectureNotWritten:   "write",
	WarnActivationFailed:         "activate",
	WarnRejectedPathCalls:        "analyse",
	WarnIssueBodyTruncated:       "write",
	WarnReportFileNotWritten:     "report",
	WarnInputLooksLikePath:       "input",
	WarnOutputNotWritten:         "emit",
	WarnOutputMatchesReportFile:  "emit",
	WarnEventsFileNotWritten:     "report",
	WarnEffortClamped:            "preflight",
	WarnReviewNotRun:             "review",
	WarnResolveNotRun:            "resolve",
	WarnUnmetRequirements:        "review",
	WarnDeviationNotTracked:      "land",
	WarnFixNotProven:             "verify",
	WarnRelevantFilesUnavailable: "prd",
	WarnRepoMapBuildFailed:       "repo_map",
	WarnCodeSearchUnavailable:    "preflight",
	WarnUntrackedFilesLeftAlone:  "commit",
	WarnUnlistedFileCommitted:    "commit",
}

// WarnStage looks up the stage recorded for a declared WarnCode. ok is false
// for a code with no table entry.
func WarnStage(code WarnCode) (string, bool) {
	stage, ok := warnStages[code]
	return stage, ok
}

// DeclaredWarnCodes returns every WarnCode constant declared above, for a
// test to walk against warnStages.
func DeclaredWarnCodes() []WarnCode {
	return []WarnCode{
		WarnInputTruncated,
		WarnCommentsUnreadable,
		WarnCommentsTruncated,
		WarnNoVerifyCommand,
		WarnCriteriaUnmet,
		WarnCommitNotParked,
		WarnCheckoutNotRestored,
		WarnPullRequestNotOpened,
		WarnCommentNotPosted,
		WarnSpecEditReverted,
		WarnStateNotSaved,
		WarnGateEdited,
		WarnDocsNotUpdated,
		WarnToolErrors,
		WarnScratchFileRemoved,
		WarnScratchFileSuspected,
		WarnDraftPackage,
		WarnUpstreamMissing,
		WarnParkedAttemptDiscarded,
		WarnSpecValidationWarning,
		WarnSpecsDirUnreadable,
		WarnProjectLanguageUnknown,
		WarnNameFlagIgnored,
		WarnScopeRenamed,
		WarnScopeCountMismatch,
		WarnSplitPlanForeign,
		WarnSplitPlanUnreadable,
		WarnSplitPlanStale,
		WarnSplitPlanUpdateFailed,
		WarnSplitPlanNotRemoved,
		WarnArchitectureNotWritten,
		WarnActivationFailed,
		WarnRejectedPathCalls,
		WarnIssueBodyTruncated,
		WarnReportFileNotWritten,
		WarnInputLooksLikePath,
		WarnOutputNotWritten,
		WarnOutputMatchesReportFile,
		WarnEventsFileNotWritten,
		WarnEffortClamped,
		WarnReviewNotRun,
		WarnResolveNotRun,
		WarnUnmetRequirements,
		WarnDeviationNotTracked,
		WarnFixNotProven,
		WarnRelevantFilesUnavailable,
		WarnRepoMapBuildFailed,
		WarnCodeSearchUnavailable,
		WarnUntrackedFilesLeftAlone,
		WarnUnlistedFileCommitted,
	}
}
