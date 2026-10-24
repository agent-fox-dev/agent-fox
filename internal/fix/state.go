package fix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// State is fix.json: everything the later steps of a run need, written once the
// preflight and the brief are done.
type State struct {
	RunID          string        `json:"run_id"`
	Input          StateInput    `json:"input"`
	Issue          *StateIssue   `json:"issue"`
	Target         StateTarget   `json:"target"`
	Base           string        `json:"base"`
	BaseHead       string        `json:"base_head"`
	Table          project.Table `json:"table"`
	BaselineGateID string        `json:"baseline_gate_id"`
	BaselinePassed bool          `json:"baseline_passed"`
	BaselineMS     int64         `json:"baseline_ms"`
	NoVerify       bool          `json:"no_verify"`
	PrefixSHA256   string        `json:"prefix_sha256"`
	// PrefixBytes is the length of the prefix in brief.md, which the task block
	// follows, so that a later step can split the brief where it was rendered.
	PrefixBytes int `json:"prefix_bytes"`
	// Suite and Programs are what the gate detected beside the table: the command
	// forms that count as the full test suite, and the programs the checks run. The
	// box's guard.json is made of them.
	Suite    [][]string `json:"suite"`
	Programs []string   `json:"programs"`
	Context  []string   `json:"context"`
	Rounds   int        `json:"rounds"`
	// Brain, CostUSD and CostSource are what the rounds of the run have used so far: the
	// footer of a pull request says it.
	Brain      string  `json:"brain"`
	CostUSD    float64 `json:"cost_usd"`
	CostSource string  `json:"cost_source"`
}

// AddUsage adds what a round used: the cost, and the brain it was the cost of. The source
// of the total is the best any round had: measured, then reported, then none.
func (s *State) AddUsage(u toolio.Usage) {
	s.Brain, s.CostUSD = u.Brain, s.CostUSD+u.CostUSD
	rank := map[string]int{"": 0, toolio.CostNone: 1, toolio.CostReported: 2, toolio.CostMeasured: 3}
	if rank[u.CostSource] > rank[s.CostSource] {
		s.CostSource = u.CostSource
	}
}

// StateInput is the input of a run, and the key that finds it again.
type StateInput struct {
	Kind   string `json:"kind"`
	Origin string `json:"origin"`
	Key    string `json:"key"`
}

// StateTarget is the repository a pull request is opened in, with its host, which a
// later step builds its forge client from. It is empty when there is none.
type StateTarget struct {
	Repo string `json:"repo"`
	Host string `json:"host"`
}

// StateIssue is the issue of a run: its URL, its title and its number, which names the branch.
type StateIssue struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Number int    `json:"number"`
}

// StateFile is the name of the file, in the run directory.
const StateFile = "fix.json"

// InputKey identifies an input across runs: the URL of the issue, the absolute path
// of the file, or the SHA-256 of the text, which is the body of a text or a standard
// input. The questions of an earlier run are found by it.
func InputKey(kind, origin, body string) string {
	switch kind {
	case "issue", "file":
		return origin
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// GateMS is how long the checks of a gate run took, added up: the time a measuring of
// a change is expected to need.
func GateMS(res *checks.Result) int64 {
	var ms int64
	if res != nil {
		for _, c := range res.Checks {
			ms += c.DurationMS
		}
	}
	return ms
}

// Marshal is the content of fix.json.
func (s State) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", StateFile, err)
	}
	return append(data, '\n'), nil
}

// Write stores the state in the run directory, atomically: a reader sees the file
// as it was or as it is, and never half of it.
func (s State) Write(runDir string) error {
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	return agentdir.WriteFileAtomic(filepath.Join(runDir, StateFile), data)
}

// LoadState reads fix.json of a run directory.
func LoadState(runDir string) (State, error) {
	data, err := os.ReadFile(filepath.Join(runDir, StateFile))
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("%s: %w", filepath.Join(runDir, StateFile), err)
	}
	return s, nil
}
