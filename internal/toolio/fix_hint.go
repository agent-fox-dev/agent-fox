package toolio

import (
	"errors"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"github.com/agentfox/agentkit-go/provider/google"
	"github.com/agentfox/agentkit-go/provider/ollama"
	"github.com/agentfox/agentkit-go/provider/openai"
)

// FixHintFor computes a machine-usable remedy for the given failure category,
// stage, resolved bounds and underlying error. It returns nil for any category
// outside budget, max_turns, no_result, auth, model and single-flag usage errors.
func FixHintFor(category, stage string, bounds agentrun.Bounds, err error) *FixHint {
	switch category {
	case "budget":
		if stage == "budget" {
			var current float64
			var tb interface{ TotalBudgetUSD() float64 }
			if errors.As(err, &tb) {
				current = tb.TotalBudgetUSD()
			}
			return &FixHint{
				Flag:    "--total-budget",
				Current: current,
				Suggest: current * 2,
			}
		}
		curr := bounds.MaxBudgetUSD
		return &FixHint{
			Flag:    "--budget",
			Current: curr,
			Suggest: curr * 2,
		}

	case "max_turns":
		curr := float64(bounds.MaxTurns)
		return &FixHint{
			Flag:    "--max-turns",
			Current: curr,
			Suggest: curr * 2,
		}

	case "no_result":
		var stopReason core.RunStopReason
		var srHolder interface{ RunStopReason() core.RunStopReason }
		var srHolderStr interface{ RunStopReason() string }
		if errors.As(err, &srHolder) {
			stopReason = srHolder.RunStopReason()
		} else if errors.As(err, &srHolderStr) {
			stopReason = core.RunStopReason(srHolderStr.RunStopReason())
		}
		if stopReason == "" && err != nil {
			msg := err.Error()
			if strings.Contains(msg, string(core.RunStopBudgetExceeded)) {
				stopReason = core.RunStopBudgetExceeded
			} else if strings.Contains(msg, string(core.RunStopMaxTurns)) {
				stopReason = core.RunStopMaxTurns
			}
		}
		switch stopReason {
		case core.RunStopBudgetExceeded:
			curr := bounds.MaxBudgetUSD
			return &FixHint{
				Flag:    "--budget",
				Current: curr,
				Suggest: curr * 2,
			}
		case core.RunStopMaxTurns:
			curr := float64(bounds.MaxTurns)
			return &FixHint{
				Flag:    "--max-turns",
				Current: curr,
				Suggest: curr * 2,
			}
		default:
			return nil
		}

	case "auth":
		var msg string
		if err != nil {
			msg = strings.ToLower(err.Error())
		}
		if strings.Contains(msg, "gitlab") {
			return &FixHint{Env: []string{"GITLAB_TOKEN"}}
		}
		if strings.Contains(msg, "github") || stage == "land" {
			return &FixHint{Env: []string{"GITHUB_TOKEN", "GH_TOKEN"}}
		}

		var mHolder interface{ AuthModel() *core.Model }
		var mHolder2 interface{ Model() *core.Model }
		if errors.As(err, &mHolder) && mHolder.AuthModel() != nil {
			return &FixHint{Env: agentrun.CredentialVars(mHolder.AuthModel())}
		} else if errors.As(err, &mHolder2) && mHolder2.Model() != nil {
			return &FixHint{Env: agentrun.CredentialVars(mHolder2.Model())}
		}

		if strings.Contains(msg, "anthropic") {
			return &FixHint{Env: agentrun.CredentialVars(&core.Model{Provider: "anthropic", API: anthropic.API})}
		}
		if strings.Contains(msg, "openai") {
			return &FixHint{Env: agentrun.CredentialVars(&core.Model{Provider: "openai", API: openai.API})}
		}
		if strings.Contains(msg, "google") || strings.Contains(msg, "gemini") {
			return &FixHint{Env: agentrun.CredentialVars(&core.Model{Provider: "google", API: google.API})}
		}
		if strings.Contains(msg, "ollama") {
			return &FixHint{Env: agentrun.CredentialVars(&core.Model{Provider: "ollama", API: ollama.API})}
		}
		return nil

	case "model":
		return &FixHint{
			Flag:  "--model",
			Valid: []string{"SIMPLE", "STANDARD", "ADVANCED"},
		}

	case "usage":
		if err == nil {
			return nil
		}
		msg := err.Error()
		if strings.Contains(msg, "--overwrite") && (strings.Contains(msg, "--repo") || strings.Contains(msg, "--label")) {
			return &FixHint{Flag: "--overwrite"}
		}
		if strings.Contains(msg, "--no-verify") && strings.Contains(msg, "--verify") {
			return &FixHint{Flag: "--no-verify"}
		}
		return nil

	default:
		return nil
	}
}
