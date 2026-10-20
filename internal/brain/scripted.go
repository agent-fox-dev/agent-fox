package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
)

func init() {
	Default.Register(Entry{Name: ScriptedName, CostSource: CostNone, New: newScripted})
}

// The results a step can be expected to have.
const (
	resultOK       = "ok"
	resultError    = "error"
	resultDenied   = "denied"
	resultAccepted = "accepted"
	resultRefused  = "refused"
)

// step is one step of a script: a tool call, or a submission, and what it is
// expected to come to.
type step struct {
	Tool   *string          `json:"tool"`
	Args   json.RawMessage  `json:"args"`
	Submit *json.RawMessage `json:"submit"`
	Expect string           `json:"expect"`
}

// scriptFile is the file --script names.
type scriptFile struct {
	Steps []step `json:"steps"`
}

// scripted is a brain that replays a script: the steps of a run, in order, with
// no model. It is a test oracle (a step says what it expects) and a way to run
// af without a model.
type scripted struct {
	file  string
	steps []step
}

// newScripted reads and checks the script, so that a script that cannot be
// used is a usage failure before any box opens.
func newScripted(opts Options) (Brain, error) {
	if opts.Script == "" {
		return nil, &toolio.Failure{Category: toolio.CategoryUsage, Message: "the scripted brain needs --script: the file of steps to replay"}
	}
	usage := func(format string, args ...any) error {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("script %s: ", opts.Script) + fmt.Sprintf(format, args...)}
	}
	data, err := os.ReadFile(opts.Script)
	if err != nil {
		return nil, usage("cannot be read: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file scriptFile
	if err := dec.Decode(&file); err != nil {
		return nil, usage("is not a script, {\"steps\": [...]}: %v", err)
	}
	if _, err := dec.Token(); err == nil {
		return nil, usage("holds more than one JSON document")
	}
	for i, st := range file.Steps {
		if msg := st.check(); msg != "" {
			return nil, usage("step %d %s", i, msg)
		}
	}
	return &scripted{file: opts.Script, steps: file.Steps}, nil
}

// check says what is wrong with a step, or "".
func (s step) check() string {
	switch {
	case s.Tool != nil && s.Submit != nil:
		return "has both tool and submit: a step is one or the other"
	case s.Tool == nil && s.Submit == nil:
		return "has neither tool nor submit: a step is one or the other"
	case s.Tool != nil && *s.Tool == "":
		return "has an empty tool name"
	case s.Submit != nil && len(s.Args) > 0:
		return "is a submit step with args: only a tool step has args"
	case s.Submit != nil && !slices.Contains([]string{"", resultAccepted, resultRefused}, s.Expect):
		return fmt.Sprintf("expects %q, and a submit step expects accepted or refused", s.Expect)
	case s.Tool != nil && !slices.Contains([]string{"", resultOK, resultError, resultDenied}, s.Expect):
		return fmt.Sprintf("expects %q, and a tool step expects ok, error or denied", s.Expect)
	}
	return ""
}

// describe is a step in a few words, for the transcript.
func (s step) describe() string {
	if s.Tool != nil {
		return "tool " + *s.Tool
	}
	return "submit"
}

// Run replays the steps on the host: a tool step goes over the box's tools
// through the gate, and a submit step through the submit core against the
// session. Each step is a turn. The tools of the brief are checked first, and
// nothing runs if one is not there. An accepted submission ends the replay, with
// the steps after it listed as skipped in the transcript; so does the turn bound
// (StopReason turns, when more steps were left), and the end of the steps
// (StopReason ended). A step whose result is not the one it expects stops the
// replay with an error that names the step, what was expected and what came.
func (s *scripted) Run(ctx context.Context, b Brief) (Outcome, error) {
	if b.Box == nil {
		return Outcome{StopReason: StopError}, errors.New("the scripted brain needs a box")
	}
	if err := ValidateTools(b.Tools, b.Box.Tools()); err != nil {
		return Outcome{StopReason: StopError}, err
	}
	var tr *Transcript
	if b.TranscriptPath != "" {
		var err error
		if tr, err = NewTranscript(b.TranscriptPath); err != nil {
			return Outcome{StopReason: StopError}, fmt.Errorf("the transcript cannot be written: %w", err)
		}
		defer tr.Close()
	}
	gate, err := GateFor(ctx, b, ScriptedName, tr)
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}

	out := Outcome{CostSource: CostNone, Transcript: b.TranscriptPath}
	for i, st := range s.steps {
		if err := ctx.Err(); err != nil {
			out.StopReason = StopError
			return out, err
		}
		if b.MaxTurns > 0 && out.Turns >= b.MaxTurns {
			out.StopReason = StopTurns
			return out, nil
		}
		out.Turns++
		actual, err := s.play(ctx, gate, b, st)
		if err != nil {
			out.StopReason = StopError
			return out, err
		}
		if st.Expect != "" && st.Expect != actual {
			out.StopReason = StopError
			return out, fmt.Errorf("script %s: step %d: expected %s, actual %s", s.file, i, st.Expect, actual)
		}
		if actual == resultAccepted {
			for j := i + 1; j < len(s.steps) && tr != nil; j++ {
				tr.Skipped(j, s.steps[j].describe())
			}
			out.Submitted, out.StopReason = true, StopSubmitted
			return out, nil
		}
	}
	out.StopReason = StopEnded
	return out, nil
}

// play runs a step, and returns what it came to: ok, error or denied for a tool,
// accepted or refused for a submission.
func (s *scripted) play(ctx context.Context, gate *Gate, b Brief, st step) (string, error) {
	if st.Tool != nil {
		args := st.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		r := gate.Call(ctx, *st.Tool, args)
		switch {
		case r.OK:
			return resultOK, nil
		case r.Error == core.BlockErrorCode:
			return resultDenied, nil
		}
		return resultError, nil
	}

	start := time.Now()
	payload := []byte(*st.Submit)
	verdict := gate.Decide(b.Kind.ToolName, nil)
	_, err := submission.Submit(ctx, submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}, b.Kind, payload, time.Now())
	var refused *submission.Refused
	switch {
	case err == nil:
		gate.Record(b.Kind.ToolName, payload, verdict, true, "the submission was accepted", time.Since(start))
		return resultAccepted, nil
	case errors.As(err, &refused):
		gate.Record(b.Kind.ToolName, payload, verdict, false, refused.Error(), time.Since(start))
		return resultRefused, nil
	}
	return "", err
}
