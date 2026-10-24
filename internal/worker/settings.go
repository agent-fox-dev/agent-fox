package worker

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// Settings are what an attempt runs with: the hub variables of its workspace, read at the start
// of the attempt, each with its default and its range. A variable that is unset or empty takes
// its default, an unknown one is ignored, and one out of its range refuses the attempt.
type Settings struct {
	// Brain is agentkit, the one brain of the service. The scripted brain of the tests is not one a
	// variable may name: it needs a script file on the worker.
	Brain string
	// Model is AF_MODEL, or the catalog's default Anthropic model. The session names it, the brain
	// runs it and the usage record carries it.
	Model string
	// Budget is AF_MAX_BUDGET_USD in dollars and MaxTurns AF_MAX_TURNS.
	Budget   float64
	MaxTurns int
	// Timeout is AF_TIMEOUT, from a minute to a day.
	Timeout time.Duration
	// MaxAttempts is AF_MAX_ATTEMPTS: the most attempts a round may make, which the jobs layer
	// bounds.
	MaxAttempts int
	// Profile is AF_SANDBOX_PROFILE, and empty is hub's default.
	Profile string
	// Land is AF_FIX_LAND: pr, branch or none, and patch, which a fix refuses.
	Land string
	// TriggersOn is AF_POLL_INTERVAL read with the poll's values: the label triggers are on unless it is a zero duration.
	// No value fails an attempt: the poll judges the rest.
	TriggersOn bool
}

// The bounds of the variables.
const (
	minTimeout     = time.Minute
	maxTimeout     = 24 * time.Hour
	minMaxAttempts = 1
	maxMaxAttempts = 10
)

// varError is the fault of a variable: it is out of its range.
type varError struct {
	Name, Value, Want string
}

func (e *varError) Error() string {
	return fmt.Sprintf("the hub variable %s is %q, and it must be %s", e.Name, e.Value, e.Want)
}

// resolveSettings reads the variables the kind of job looks at. A variable of the other kind (the
// brain of the fix for a triage, the landing) is not looked at.
func resolveSettings(kind jobs.Kind, vars map[string]string) (Settings, *varError) {
	s := Settings{
		Brain: "agentkit", Model: catalog.Default().DefaultModelID("anthropic"), Budget: 2, MaxTurns: 100,
		Timeout: 15 * time.Minute, MaxAttempts: defaultMaxAttempts, Land: "pr", TriggersOn: triggersOn(vars["AF_POLL_INTERVAL"]),
	}
	if kind == jobs.KindFix {
		s.Budget, s.MaxTurns, s.Timeout = 10, 150, 45*time.Minute
	}
	if v := vars["AF_MODEL"]; v != "" {
		s.Model = v
	}
	if v := vars["AF_MAX_BUDGET_USD"]; v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			return s, &varError{"AF_MAX_BUDGET_USD", v, "a finite number of dollars above 0"}
		}
		s.Budget = n
	}
	if v := vars["AF_MAX_TURNS"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return s, &varError{"AF_MAX_TURNS", v, "a whole number of at least 1"}
		}
		s.MaxTurns = n
	}
	if v := vars["AF_TIMEOUT"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minTimeout || d > maxTimeout {
			return s, &varError{"AF_TIMEOUT", v, "a duration from 1m to 24h"}
		}
		s.Timeout = d
	}
	if v := vars["AF_MAX_ATTEMPTS"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minMaxAttempts || n > maxMaxAttempts {
			return s, &varError{"AF_MAX_ATTEMPTS", v, "a whole number from 1 to 10"}
		}
		s.MaxAttempts = n
	}
	s.Profile = vars["AF_SANDBOX_PROFILE"]
	if kind == jobs.KindFix {
		if v := vars["AF_FIX_LAND"]; v != "" {
			if v != "pr" && v != "branch" && v != "none" && v != landPatch {
				return s, &varError{"AF_FIX_LAND", v, "pr, branch or none"}
			}
			s.Land = v
		}
	}
	return s, nil
}

// triggersOn reads AF_POLL_INTERVAL as the label poll does (internal/service/poll, parseInterval), as far as the worker
// needs it: a zero duration turns the label triggers off, and anything else, an invalid value included, leaves them on.
func triggersOn(value string) bool {
	d, err := time.ParseDuration(strings.TrimSpace(value))
	return err != nil || d != 0
}

// landPatch is the landing of a fix that the worker refuses: patches belong to carry-patch
// workspaces, which a later release serves.
const landPatch = "patch"

// options are the settings as the options of the loop: the brain in the gateway box, its bounds,
// and for a fix where the change goes. The timeout is the call's.
func (s Settings) options(kind jobs.Kind) afcmd.LoopOptions {
	o := afcmd.LoopOptions{Brain: s.Brain, Sandbox: box.GatewayName, Budget: s.Budget, MaxTurns: s.MaxTurns}
	if kind == jobs.KindFix {
		o.Land = s.Land
	}
	return o
}
