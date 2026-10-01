package toolio

// PreflightCheck is one check --preflight performed and its outcome. OK can
// be false without the run having refused: an entry for a check that is
// advisory rather than refusing (the verification command was not detected;
// the baseline currently fails) reports its own outcome honestly, and is
// only ever present at all because the run reached the point of reporting
// it. A check whose failure refuses the run never produces an entry here:
// the caller gets the same failing envelope the ordinary run would have
// produced instead.
type PreflightCheck struct {
	Check  string `json:"check" description:"the name of the check that was performed, such as clean_tree or forge_credential" trust:"fact"`
	OK     bool   `json:"ok" description:"whether the check passed; false only for an advisory check, since a refusing check that fails fails the run instead"`
	Detail string `json:"detail,omitempty" description:"a fact the program measured about the check, such as a branch name or a command's pass or fail summary" trust:"fact"`
}

// Estimate bounds what the real run would spend at most, from what
// --preflight already knows without calling a model.
type Estimate struct {
	Phases               int     `json:"phases" description:"the number of model phases the plan on disk already decides on; a lower bound, since a repair phase or a split a model decides on is not counted"`
	MaxTurnsPerPhase     int     `json:"max_turns_per_phase" description:"the turn ceiling applied to each phase"`
	MaxBudgetPerPhaseUSD float64 `json:"max_budget_per_phase_usd" description:"the spend ceiling applied to each phase, in US dollars"`
	MaxTotalUSD          float64 `json:"max_total_usd" description:"the most the phases counted in phases could spend in total, in US dollars"`
}
