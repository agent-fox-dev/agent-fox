package toolio

// WarnCode identifies one warning cause. Every declared constant has exactly
// one stage recorded in warnStages below, so a code cannot be used without a
// stage, and cannot be recorded against two different stages by accident.
type WarnCode string

// Declared warning codes. This is not exhaustive by design (05-REQ-5.6): a
// call site that fits none of these gets a new constant added here and to
// warnStages, never a bare string.
const (
	WarnInputTruncated         WarnCode = "input_truncated"
	WarnCommentsUnreadable     WarnCode = "comments_unreadable"
	WarnNoVerifyCommand        WarnCode = "no_verify_command"
	WarnCriteriaUnmet          WarnCode = "criteria_unmet"
	WarnCommitNotParked        WarnCode = "commit_not_parked"
	WarnCheckoutNotRestored    WarnCode = "checkout_not_restored"
	WarnPullRequestNotOpened   WarnCode = "pull_request_not_opened"
	WarnCommentNotPosted       WarnCode = "comment_not_posted"
	WarnSpecEditReverted       WarnCode = "spec_edit_reverted"
	WarnStateNotSaved          WarnCode = "state_not_saved"
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
	WarnRejectedPathCalls      WarnCode = "rejected_path_calls"
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
)

// warnStages is the single table mapping every declared WarnCode to the
// pipeline stage that raises it. This is what Run.Warn looks Stage up from,
// rather than accepting it as a call-site parameter, so a code can never be
// recorded against two different stages.
var warnStages = map[WarnCode]string{
	WarnInputTruncated:         "input",
	WarnCommentsUnreadable:     "input",
	WarnNoVerifyCommand:        "preflight",
	WarnCriteriaUnmet:          "implement",
	WarnCommitNotParked:        "park",
	WarnCheckoutNotRestored:    "park",
	WarnPullRequestNotOpened:   "land",
	WarnCommentNotPosted:       "report",
	WarnSpecEditReverted:       "task",
	WarnStateNotSaved:          "park",
	WarnDraftPackage:           "preflight",
	WarnUpstreamMissing:        "preflight",
	WarnParkedAttemptDiscarded: "preflight",
	WarnSpecValidationWarning:  "preflight",
	WarnSpecsDirUnreadable:     "preflight",
	WarnProjectLanguageUnknown: "preflight",
	WarnNameFlagIgnored:        "usage",
	WarnScopeRenamed:           "prd",
	WarnScopeCountMismatch:     "prd",
	WarnSplitPlanForeign:       "split",
	WarnSplitPlanUnreadable:    "split",
	WarnSplitPlanStale:         "split",
	WarnSplitPlanUpdateFailed:  "split",
	WarnSplitPlanNotRemoved:    "split",
	WarnArchitectureNotWritten: "write",
	WarnActivationFailed:       "activate",
	WarnRejectedPathCalls:      "analyse",
	WarnReportFileNotWritten:   "report",
	WarnInputLooksLikePath:     "input",
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
		WarnNoVerifyCommand,
		WarnCriteriaUnmet,
		WarnCommitNotParked,
		WarnCheckoutNotRestored,
		WarnPullRequestNotOpened,
		WarnCommentNotPosted,
		WarnSpecEditReverted,
		WarnStateNotSaved,
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
		WarnReportFileNotWritten,
		WarnInputLooksLikePath,
	}
}
