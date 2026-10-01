package codeimpl

// summaryTask is one task reduced to what a caller acts on next, kept in
// impl's --detail summary view (06-REQ-3.3).
type summaryTask struct {
	ID      int    `json:"id"`
	Outcome string `json:"outcome"`
	Commit  string `json:"commit,omitempty"`
	Verdict string `json:"verdict,omitempty"`
}

// summaryResult is the trimmed view of Result kept under --detail summary
// (06-REQ-3.3). The top-level verification — the gate's checks and their
// tail output — is included only when the run's stopping verdict is not one
// landable() (codeimpl/gate.go) reports true for, the same predicate the
// pipeline itself uses to decide whether to land. Every other field of
// Result is still computed exactly as under --detail full, and is still
// written in full to the report file (06-REQ-3.5).
type summaryResult struct {
	Stage          string        `json:"stage"`
	SpecDir        string        `json:"spec_dir"`
	SpecID         string        `json:"spec_id"`
	SpecName       string        `json:"spec_name"`
	Title          string        `json:"title"`
	Status         string        `json:"status"`
	Branch         string        `json:"branch,omitempty"`
	TasksTotal     int           `json:"tasks_total"`
	TasksDone      int           `json:"tasks_done"`
	TasksSkipped   int           `json:"tasks_skipped"`
	TasksRemaining int           `json:"tasks_remaining"`
	Tasks          []summaryTask `json:"tasks"`
	Verdict        string        `json:"verdict"`
	PullRequestURL string        `json:"pull_request_url,omitempty"`
	Verification   *GateResult   `json:"verification,omitempty"`
	Detail         string        `json:"detail"`
}

// SummaryView implements toolio.Summarizable.
func (r *Result) SummaryView() any {
	tasks := make([]summaryTask, len(r.Tasks))
	for i, t := range r.Tasks {
		tasks[i] = summaryTask{ID: t.ID, Outcome: t.Outcome, Commit: t.Commit, Verdict: t.Verdict}
	}
	s := summaryResult{
		Stage:          r.Stage,
		SpecDir:        r.SpecDir,
		SpecID:         r.SpecID,
		SpecName:       r.SpecName,
		Title:          r.Title,
		Status:         r.Status,
		Branch:         r.Branch,
		TasksTotal:     r.TasksTotal,
		TasksDone:      r.TasksDone,
		TasksSkipped:   r.TasksSkipped,
		TasksRemaining: r.TasksRemaining,
		Tasks:          tasks,
		Verdict:        r.Verdict,
		PullRequestURL: r.PullRequestURL,
		Detail:         "summary",
	}
	if !landable(r.Verdict, len(r.Gate) == 0) {
		v := r.Verification
		s.Verification = &v
	}
	return s
}
