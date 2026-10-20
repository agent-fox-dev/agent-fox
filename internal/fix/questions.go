package fix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// runIDFormat is what the name of a run directory looks like.
var runIDFormat = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)

// FindEarlier finds, for each answer of a run, the question it answers: the i-th
// answer goes with the question of the newest run under dir/.agent-fox/run whose
// fix.json has the input key, whose envelope ends in needs_human, and which carried
// exactly i answers. A bare answer such as "B" means nothing to a brain without the
// question it answers, and the shell keeps no state between two invocations, so the
// run directories are where the question is. The run exclude is the one being made,
// and is skipped. An answer no earlier run asked about is given alone.
func FindEarlier(dir, key string, answers []string, exclude string) []Earlier {
	earlier := make([]Earlier, len(answers))
	if len(answers) == 0 {
		return earlier
	}
	runs := filepath.Join(dir, ".agent-fox", "run")
	entries, _ := os.ReadDir(runs)
	var ids []string
	for _, e := range entries {
		if e.IsDir() && runIDFormat.MatchString(e.Name()) && e.Name() != exclude {
			ids = append(ids, e.Name())
		}
	}
	slices.Sort(ids)
	slices.Reverse(ids)

	// The newest question for each count of answers, found once.
	asked := map[int]*Earlier{}
	for _, id := range ids {
		run := filepath.Join(runs, id)
		state, err := LoadState(run)
		if err != nil || state.Input.Key != key || asked[len(state.Context)] != nil || len(state.Context) >= len(answers) {
			continue
		}
		if q := questionOf(run); q != nil {
			asked[len(state.Context)] = q
		}
	}
	for i, answer := range answers {
		earlier[i] = Earlier{Answer: answer}
		if q := asked[i]; q != nil {
			earlier[i].Question, earlier[i].Options, earlier[i].Found = q.Question, q.Options, true
		}
	}
	return earlier
}

// questionOf is the question of the envelope of a run directory, or nil when the run
// did not end in a question.
func questionOf(run string) *Earlier {
	data, err := os.ReadFile(filepath.Join(run, "envelope.json"))
	if err != nil {
		return nil
	}
	var env struct {
		Status     string `json:"status"`
		NeedsHuman *struct {
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"needs_human"`
	}
	if json.Unmarshal(data, &env) != nil || env.Status != "needs_human" || env.NeedsHuman == nil || env.NeedsHuman.Question == "" {
		return nil
	}
	q := &Earlier{Question: env.NeedsHuman.Question}
	for _, o := range env.NeedsHuman.Options {
		q.Options = append(q.Options, o.Label)
	}
	return q
}
