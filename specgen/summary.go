package specgen

// summaryValidation is ValidationReport reduced to what a caller acts on
// next: whether the package validates and what to fix if not. Dropped:
// warning_count and warnings (06-REQ-3.4).
type summaryValidation struct {
	Valid      bool             `json:"valid"`
	ErrorCount int              `json:"error_count"`
	Errors     []ValidationItem `json:"errors,omitempty"`
}

// summaryTrace is TraceReport reduced to the gaps a caller would act on.
// Dropped: criteria_covered and paths_covered (06-REQ-3.4).
type summaryTrace struct {
	CriteriaUncovered []string `json:"criteria_uncovered,omitempty" trust:"fact"`
	PathsUncovered    []string `json:"paths_uncovered,omitempty" trust:"fact"`
	TestsUnowned      []string `json:"tests_unowned,omitempty" trust:"fact"`
}

// summaryResult is the trimmed view of Result kept under --detail summary
// (06-REQ-3.4). follow_on_specs and split_plan are dropped: split[] already
// names every scope with its status, which is what a caller needs to know
// whether to run spec again. Every other field of Result is still computed
// exactly as under --detail full, and is still written in full to the
// report file (06-REQ-3.5).
type summaryResult struct {
	SpecDir       string            `json:"spec_dir" trust:"fact"`
	SpecID        string            `json:"spec_id" trust:"fact"`
	SpecName      string            `json:"spec_name" trust:"fact"`
	Status        string            `json:"status" trust:"fact"`
	Artifacts     []string          `json:"artifacts" trust:"fact"`
	Validation    summaryValidation `json:"validation"`
	Traceability  summaryTrace      `json:"traceability"`
	OpenQuestions []OpenQuestion    `json:"open_questions,omitempty"`
	Split         []ScopeReport     `json:"split,omitempty"`
	Detail        string            `json:"detail" trust:"fact"`
}

// SummaryView implements toolio.Summarizable. It is defined on Result
// (rather than on the embedded Package) because Split lives on Result
// alone.
func (r *Result) SummaryView() any {
	return summaryResult{
		SpecDir:   r.SpecDir,
		SpecID:    r.SpecID,
		SpecName:  r.SpecName,
		Status:    r.Status,
		Artifacts: r.Package.Artifacts,
		Validation: summaryValidation{
			Valid:      r.Validation.Valid,
			ErrorCount: r.Validation.ErrorCount,
			Errors:     r.Validation.Errors,
		},
		Traceability: summaryTrace{
			CriteriaUncovered: r.Traceability.CriteriaUncovered,
			PathsUncovered:    r.Traceability.PathsUncovered,
			TestsUnowned:      r.Traceability.TestsUnowned,
		},
		OpenQuestions: r.OpenQuestions,
		Split:         r.Split,
		Detail:        "summary",
	}
}
