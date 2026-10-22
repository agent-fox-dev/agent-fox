package fakerunner

import (
	"encoding/json"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Env is the JSON of an envelope with the status, as a command writes it: indented,
// with a newline at the end, and with the arrays empty and the usage of no brain, so
// that it is not the canonical JSON of the bytes a decoder gives back. mods change the
// envelope before it is written.
func Env(status toolio.Status, mods ...func(*toolio.Envelope)) []byte {
	e := toolio.Envelope{
		SchemaVersion: toolio.SchemaVersion,
		Tool:          "fix",
		Status:        status,
		ExitCode:      toolio.ExitCode(status),
		Summary:       string(status),
		Input:         toolio.InputInfo{Kind: toolio.KindIssue, Origin: "fake"},
		Warnings:      []toolio.Warning{},
		Artifacts:     []toolio.Artifact{},
		SideEffects:   []toolio.SideEffect{},
		Next:          []toolio.Next{},
		Usage:         toolio.Usage{CostSource: toolio.CostNone},
		StartedAt:     "2026-10-22T08:00:00.000Z",
		FinishedAt:    "2026-10-22T08:00:01.000Z",
		DurationMS:    1000,
	}
	for _, mod := range mods {
		mod(&e)
	}
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("fakerunner: the envelope cannot be encoded: %v", err))
	}
	return append(raw, '\n')
}

// WithTool sets the tool of the envelope.
func WithTool(tool string) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Tool = tool }
}

// WithSummary sets the summary of the envelope.
func WithSummary(summary string) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Summary = summary }
}

// WithError sets the error of the envelope.
func WithError(category, message string, retryable bool) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) {
		e.Error = &toolio.ErrorInfo{Category: category, Message: message, Retryable: retryable}
	}
}

// WithQuestion makes the envelope ask a question with the options, which the labels
// give, lettered A, B, … in order.
func WithQuestion(question string, labels ...string) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) {
		nh := &toolio.NeedsHuman{Question: question, Options: []toolio.Option{}, Resume: toolio.Resume{Argv: []string{"af", "fix"}, Flag: "--answer"}}
		for i, label := range labels {
			nh.Options = append(nh.Options, toolio.Option{ID: string(rune('A' + i)), Label: label})
		}
		e.NeedsHuman = nh
	}
}

// WithOutcome sets the outcome in the result of the envelope, as a fix does ("parked",
// "landed", …).
func WithOutcome(outcome string) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Result = map[string]any{"outcome": outcome} }
}

// WithArtifacts sets the artifacts of the envelope.
func WithArtifacts(artifacts ...toolio.Artifact) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Artifacts = artifacts }
}

// WithSideEffects sets the side effects of the envelope.
func WithSideEffects(effects ...toolio.SideEffect) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.SideEffects = effects }
}

// WithUsage sets the usage of the envelope.
func WithUsage(u toolio.Usage) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Usage = u }
}
