package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// maxMessage is the longest message of an error that the layer makes an envelope of.
const maxMessage = 1024

// synthesize makes the envelope of an attempt that ended without a usable one: a
// failed envelope of the schema version, with the tool of the job's kind, empty
// arrays, no cost, and the error.
func synthesize(kind Kind, now time.Time, failure toolio.ErrorInfo) toolio.Envelope {
	if failure.Category == "" {
		failure.Category = toolio.CategoryInternal
	}
	if strings.TrimSpace(failure.Message) == "" {
		failure.Message = "the attempt failed (" + failure.Category + ")"
	}
	failure.Message = clip(failure.Message, maxMessage)
	at := formatTime(now)
	return toolio.Envelope{
		SchemaVersion: toolio.SchemaVersion,
		Tool:          string(kind),
		Status:        toolio.StatusFailed,
		ExitCode:      toolio.ExitCode(toolio.StatusFailed),
		Summary:       failure.Message,
		Input:         toolio.InputInfo{Kind: toolio.KindNone},
		Error:         &failure,
		Warnings:      []toolio.Warning{},
		Artifacts:     []toolio.Artifact{},
		SideEffects:   []toolio.SideEffect{},
		Next:          []toolio.Next{},
		Usage:         toolio.Usage{CostSource: toolio.CostNone},
		StartedAt:     at,
		FinishedAt:    at,
	}
}

// clip cuts s to at most n bytes, at a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// encodeEnvelope writes an envelope the way a command does: indented, with a newline
// at the end.
func encodeEnvelope(env toolio.Envelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseEnvelope reads the envelope a runner returned, and says what is wrong with it
// when it cannot be used: it is not an envelope, its status is not one of the five,
// or it asks a question without a question.
func parseEnvelope(raw []byte) (toolio.Envelope, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return toolio.Envelope{}, "the runner returned no envelope"
	}
	var env toolio.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return toolio.Envelope{}, "it is not an envelope: " + err.Error()
	}
	switch env.Status {
	case toolio.StatusDone, toolio.StatusFailed, toolio.StatusUsage, toolio.StatusUnverified:
	case toolio.StatusNeedsHuman:
		if env.NeedsHuman == nil || strings.TrimSpace(env.NeedsHuman.Question) == "" {
			return toolio.Envelope{}, "its status is needs_human and it has no question"
		}
	default:
		return toolio.Envelope{}, fmt.Sprintf("its status %q is not one of done, failed, usage, needs_human and unverified", env.Status)
	}
	return env, ""
}

// outcomeOf is the outcome in the result of an envelope ("parked" for a fix that
// parked its change), or "".
func outcomeOf(env toolio.Envelope) string {
	result, ok := env.Result.(map[string]any)
	if !ok {
		return ""
	}
	outcome, _ := result["outcome"].(string)
	return outcome
}
