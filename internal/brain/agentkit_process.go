package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// stderrTail is how much of the errors of a process an error of the brain carries.
const stderrTail = 2 << 10

// driverInBox says the agentkit brain these options make runs inside the box: the
// provider isolates, and the driver was not asked to borrow the box's hands. A provider
// that is not named, or that the registry does not know, is the host.
func driverInBox(opts Options) bool {
	provider, err := box.Default.Lookup(opts.Provider)
	return err == nil && PlacementOf(Entry{DriverInBox: true}, provider, opts.BorrowedHands) == PlacementBox
}

// agentkitInBox runs the driver of AgentKit inside the box as af <agent> run, which is the
// same brain as the one on the host, in a process that has the tools of the directory
// itself and the key of the model vendor, and nothing else of the host: the model and
// the driver are in the box, and what comes back is its envelope.
type agentkitInBox struct {
	proc  *Process
	model string
}

// newAgentkitInBox makes the brain. The key of the model vendor is needed in the box,
// so it is needed here, before any box opens.
func newAgentkitInBox(opts Options) (Brain, error) {
	if v, ok := opts.lookup()(VendorKeyVar); !ok || v == "" {
		return nil, &toolio.Failure{
			Category: toolio.CategoryAuth,
			Message:  "the agentkit brain needs the key of the model vendor in " + VendorKeyVar + " for the box it runs in",
			FixHint:  &toolio.FixHint{Env: VendorKeyVar},
		}
	}
	return &agentkitInBox{proc: NewProcess(opts.ProcessConfig()), model: opts.Model}, nil
}

// Run runs af <agent> run --run-id <id> --dir <root> with the bounds of the brief, and
// reads the envelope it left.
func (a *agentkitInBox) Run(ctx context.Context, b Brief) (Outcome, error) {
	args := []string{b.Box.Tool(), "run", "--run-id", b.RunID, "--dir", b.Box.Root()}
	if b.MaxCostUSD > 0 {
		args = append(args, "--budget", strconv.FormatFloat(b.MaxCostUSD, 'f', -1, 64))
	}
	if b.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(b.MaxTurns))
	}
	timeout := b.Timeout
	if timeout <= 0 {
		if deadline, ok := ctx.Deadline(); ok {
			timeout = max(time.Until(deadline), time.Second).Truncate(time.Second)
		}
	}
	if timeout > 0 {
		args = append(args, "--timeout", timeout.String())
	}
	inline := map[string]string{}
	if a.model != "" {
		inline["AF_MODEL"] = a.model
	}

	res, err := a.proc.Run(ctx, b, ProcessSpec{
		Brain: AgentkitName, Program: ProgramAF, Args: args, Inline: inline, Probes: []string{ProgramAF},
	})
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}
	out, failure, ok := parseEnvelope(res.Output)
	if !ok {
		b.warn("brain_usage_unavailable", "af "+b.Box.Tool()+" run left no envelope that could be read, so the run has no cost to report")
		out = Outcome{StopReason: StopEnded, CostSource: CostNone, Submitted: submissionCarriesNonce(ctx, b)}
		if res.ExitCode != 0 && !out.Submitted {
			out.StopReason = StopError
			return out, processError("af "+b.Box.Tool()+" run", res)
		}
		return out, nil
	}
	if failure != "" && out.StopReason == StopError {
		return out, fmt.Errorf("af %s run failed: %s", b.Box.Tool(), failure)
	}
	return out, nil
}

// processError is the error of a process that ended with a code other than 0, with the
// end of its errors.
func processError(what string, res ProcessResult) error {
	tail := res.Stderr
	if len(tail) > stderrTail {
		tail = tail[len(tail)-stderrTail:]
	}
	why := fmt.Sprintf("exited with code %d", res.ExitCode)
	if res.TimedOut {
		why = "was killed when its time ran out"
	}
	return fmt.Errorf("%s %s: %s", what, why, strings.TrimSpace(string(tail)))
}

// envelope is the part of the envelope of af <agent> run that the brain reads.
type envelope struct {
	Status string `json:"status"`
	Usage  struct {
		CostUSD      float64 `json:"cost_usd"`
		CostSource   string  `json:"cost_source"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		Turns        int     `json:"turns"`
	} `json:"usage"`
	Result *struct {
		Submitted bool `json:"submitted"`
	} `json:"result"`
	Error *struct {
		Category string `json:"category"`
		Message  string `json:"message"`
		FixHint  *struct {
			Flag string `json:"flag"`
		} `json:"fix_hint"`
	} `json:"error"`
}

// parseEnvelope reads the envelope a driver in a box left. The usage is the driver's,
// measured; the claim is result.submitted; a failure of the category budget stopped at
// the bound the flag of its hint names, and any other failure is an error, with its
// message. It reports false for output that is no envelope.
func parseEnvelope(data []byte) (out Outcome, failure string, ok bool) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Status == "" {
		return Outcome{}, "", false
	}
	out = Outcome{
		CostUSD: env.Usage.CostUSD, CostSource: CostMeasured, Turns: env.Usage.Turns,
		InputTokens: int(env.Usage.InputTokens), OutputTokens: int(env.Usage.OutputTokens),
		Submitted: env.Result != nil && env.Result.Submitted, StopReason: StopEnded,
	}
	if env.Usage.CostSource == CostNone {
		out.CostSource = CostNone
	}
	if env.Error != nil {
		switch {
		case env.Error.Category == toolio.CategoryBudget && env.Error.FixHint != nil && env.Error.FixHint.Flag == "--max-turns":
			out.StopReason = StopTurns
		case env.Error.Category == toolio.CategoryBudget:
			out.StopReason = StopBudget
		default:
			out.StopReason = StopError
			failure = env.Error.Message
		}
	}
	return out, failure, true
}
