package afcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// maxPayload is the most a hook payload may be.
const maxPayload = 1 << 20

// decide is the decision of the policy; a variable so that a test can make the
// engine fail.
var decide = policy.DecideAt

// guardResult is what af guard check reports in its envelope.
type guardResult struct {
	Decision string `json:"decision"`
	Rule     string `json:"rule"`
	Reason   string `json:"reason"`
	Tool     string `json:"tool"`
	Segment  string `json:"segment"`
}

// registerGuard registers af guard check.
func registerGuard(app *toolio.App) {
	var policyPath, host string
	app.Register(toolio.Command{
		Path:    "guard check",
		Summary: "Decide whether a brain may make the tool call that is on stdin",
		Usage:   "af guard check [--dir DIR] [--policy FILE] [--host claude]  < payload.json",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&policyPath, "policy", "", "the policy `file`; default .agent-fox/guard.json in --dir, else the defaults detection gives")
			fs.StringVar(&host, "host", "", "answer in the format of this `host` instead of the envelope; the only one is claude")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 10 * time.Second,
		RawStdin:       true,
		ResultSchema:   resultSchema("guard_check"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return guardCheck(r, policyPath, host) },
	})
}

// guardCheck decides the tool call on stdin. Without --host the answer is the
// envelope, and a call that cannot be read is an error. With --host claude the
// answer is the document the host reads, and every error is a denial: a hook
// that cannot decide must not let the call through.
func guardCheck(r *toolio.Run, policyPath, host string) (toolio.Outcome, error) {
	switch host {
	case "":
		res, err := evaluate(r, policyPath)
		if err != nil {
			return toolio.Outcome{}, err
		}
		return toolio.Outcome{Status: toolio.StatusDone, Summary: guardSummary(res), Result: res}, nil
	case "claude":
		return hostAnswer(r, policyPath), nil
	}
	return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--host %q is not a host this af speaks for; the only one is claude", host)}
}

// guardSummary is the one line that says what was decided.
func guardSummary(res guardResult) string {
	switch {
	case res.Decision == policy.Allow && res.Tool == "":
		return "allow: no opinion"
	case res.Decision == policy.Allow:
		return "allow: " + res.Tool
	}
	return fmt.Sprintf("deny: %s (%s)", res.Tool, res.Rule)
}

// payload is a tool call as a host reports it.
type payload struct {
	tool      string
	input     map[string]any
	cwd       string
	noOpinion bool // the event is not one this hook is for
}

// evaluate reads the payload on stdin and decides it. A payload that cannot be
// read is a usage failure, a policy that cannot be used is a config failure.
func evaluate(r *toolio.Run, policyPath string) (guardResult, error) {
	call, err := readPayload(r.Stdin)
	if err != nil {
		return guardResult{}, err
	}
	if call.noOpinion {
		return guardResult{Decision: policy.Allow}, nil
	}
	pol, err := policy.LoadPolicy(r.Ctx, r.Dir, policyPath)
	if err != nil {
		var config *policy.ConfigError
		if errors.As(err, &config) {
			return guardResult{}, &toolio.Failure{Category: toolio.CategoryConfig, Message: config.Error()}
		}
		return guardResult{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	d := decide(pol, r.Dir, call.cwd, call.tool, call.input)
	return guardResult{Decision: d.Decision, Rule: d.Rule, Reason: d.Reason, Tool: call.tool, Segment: d.Segment}, nil
}

// readPayload reads one payload, in the form of Claude Code's hook or the
// neutral form {"tool": …, "input": …}, from at most 1 MiB of stdin. A
// payload whose hook_event_name is not PreToolUse is one this hook has no
// opinion on; a blank name names no other event, and the call is judged.
func readPayload(in io.Reader) (payload, error) {
	usage := func(format string, args ...any) (payload, error) {
		return payload{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf(format, args...)}
	}
	if in == nil {
		return usage("there is no payload on stdin")
	}
	data, err := io.ReadAll(io.LimitReader(in, maxPayload+1))
	if err != nil {
		return payload{}, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read the payload on stdin: %v", err)}
	}
	if len(data) > maxPayload {
		return usage("the payload is larger than 1 MiB")
	}

	var raw struct {
		HookEventName *string         `json:"hook_event_name"`
		ToolName      string          `json:"tool_name"`
		ToolInput     json.RawMessage `json:"tool_input"`
		Cwd           string          `json:"cwd"`
		Tool          string          `json:"tool"`
		Input         json.RawMessage `json:"input"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return usage("the payload on stdin is empty")
		}
		return usage("the payload on stdin is not a JSON object of a tool call: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return usage("the payload on stdin holds more than one JSON document")
	}
	if raw.HookEventName != nil && strings.TrimSpace(*raw.HookEventName) != "" && *raw.HookEventName != "PreToolUse" {
		return payload{noOpinion: true}, nil
	}

	tool, rawInput := raw.ToolName, raw.ToolInput
	if tool == "" {
		tool, rawInput = raw.Tool, raw.Input
	}
	if tool == "" {
		return usage("the payload names no tool: it has neither tool_name nor tool")
	}
	input := map[string]any{}
	if len(rawInput) > 0 && string(rawInput) != "null" {
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return usage("the input of the %s call is not a JSON object: %v", tool, err)
		}
	}
	return payload{tool: tool, input: input, cwd: raw.Cwd}, nil
}

// hookOutput is the document Claude Code reads from a PreToolUse hook.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// hostDocument is what the host reads: a denial with its reason, or {} for no
// opinion. The hook never says allow, which would skip the host's own
// permissions. There is no newline after it.
func hostDocument(deny bool, reason string) []byte {
	if !deny {
		return []byte("{}")
	}
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = policy.Deny
	out.HookSpecificOutput.PermissionDecisionReason = reason
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out) // strings: it cannot fail
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// hostAnswer decides in the format of the host. Every way it can go wrong is
// a denial: an error, a panic, and taking so long that the shell would give up
// on the command, which it reports as a failure the host does not act on.
func hostAnswer(r *toolio.Run, policyPath string) toolio.Outcome {
	type answer struct {
		res guardResult
		err error
	}
	done := make(chan answer, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- answer{err: fmt.Errorf("the policy engine panicked: %v", v)}
			}
		}()
		res, err := evaluate(r, policyPath)
		done <- answer{res, err}
	}()

	var out answer
	select {
	case out = <-done:
	case <-time.After(hookBudget(r)):
		out.err = errors.New("the hook did not decide in time")
	case <-r.Ctx.Done():
		out.err = errors.New("the hook was stopped before it decided")
	}

	if out.err != nil {
		reason := "af guard check failed closed: " + out.err.Error()
		return toolio.Outcome{Status: toolio.StatusDone, Summary: "deny: failed closed", Document: hostDocument(true, reason)}
	}
	return toolio.Outcome{
		Status:   toolio.StatusDone,
		Summary:  guardSummary(out.res),
		Result:   out.res,
		Document: hostDocument(out.res.Decision == policy.Deny, out.res.Reason),
	}
}

// hookBudget is how long the hook may take to decide: most of the time the
// shell gives the command. The shell throws away what a command returns after
// its deadline, so the answer must come before it.
func hookBudget(r *toolio.Run) time.Duration {
	deadline, ok := r.Ctx.Deadline()
	if !ok {
		return time.Duration(1<<63 - 1)
	}
	return time.Until(deadline) * 8 / 10
}
