package toolio

import (
	"bytes"
	"context"
	"math/rand"
	"strings"
	"testing"
)

// warnSinkRun returns a Run with an attached, active sink and the buffer the
// sink writes to.
func warnSinkRun() (*Run, *bytes.Buffer) {
	var buf bytes.Buffer
	run := NewRun("fix", "v1")
	run.AttachEvents(newEventsSink("fix", &buf))
	return run, &buf
}

// TS-07-33 (unit): the Run holds the very sink that backs Progress, attached
// once, before execute is called.
func TestTS07_33_RunHoldsTheSinkBackingProgress(t *testing.T) {
	var runSink, progressSink *eventsSink
	var reached int
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		reached++
		runSink = d.Run.eventSink()
		progressSink = d.Progress.sink()
		return ExitOK, map[string]string{"stage": "done"}, nil
	})
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
		strings.NewReader(""), &stdout, &stderr)
	if reached != 1 {
		t.Fatalf("Exec reached %d times, want 1", reached)
	}
	if runSink == nil || !runSink.Active() {
		t.Fatal("the run has no active sink attached by the time execute runs")
	}
	if runSink != progressSink {
		t.Fatalf("run sink %p is not the progress sink %p", runSink, progressSink)
	}

	// Attached exactly once: a second attach does not replace the first.
	run := NewRun("fix", "v1")
	first := newEventsSink("fix", &bytes.Buffer{})
	run.AttachEvents(first)
	run.AttachEvents(newEventsSink("fix", &bytes.Buffer{}))
	if run.eventSink() != first {
		t.Fatal("a second AttachEvents replaced the sink")
	}
}

// TS-07-34 (unit): Run.Warn emits a warning event mirroring the recorded
// warning when a sink is attached and active.
func TestTS07_34_WarnEmitsAWarningEvent(t *testing.T) {
	run, buf := warnSinkRun()
	run.Warn(WarnCommentNotPosted, "low", "could not post %s", "comment")

	evs := decodeLines(t, buf.String())
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1: %s", len(evs), buf)
	}
	ev := evs[0]
	stage, _ := WarnStage(WarnCommentNotPosted)
	want := map[string]any{
		"type": "warning", "tool": "fix", "code": string(WarnCommentNotPosted),
		"severity": "low", "stage": stage, "message": "could not post comment",
	}
	for k, v := range want {
		if ev[k] != v {
			t.Errorf("%s = %v, want %v", k, ev[k], v)
		}
	}
	if len(ev) != len(want)+2 { // plus ts and session_id
		t.Errorf("warning event has unexpected fields: %v", ev)
	}
}

// TS-07-35 (unit): a bare Run records the warning, emits nothing and does not
// panic — and neither does a nil Run or one with an inactive sink.
func TestTS07_35_BareRunWarnsWithoutEvents(t *testing.T) {
	run := NewRun("fix", "v1")
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Warn panicked: %v", r)
			}
		}()
		run.Warn(WarnCommentNotPosted, "low", "msg")
	}()
	if got := run.Warnings(); len(got) != 1 || got[0].Message != "msg" {
		t.Fatalf("Warnings() = %+v, want the one recorded warning", got)
	}

	var nilRun *Run
	nilRun.Warn(WarnCommentNotPosted, "low", "msg")

	inactive := NewRun("fix", "v1")
	inactive.AttachEvents(newEventsSink("fix"))
	inactive.Warn(WarnCommentNotPosted, "low", "msg")
	if len(inactive.Warnings()) != 1 {
		t.Fatal("an inactive sink must not stop the warning being recorded")
	}
}

// TS-07-36 (property): for any batch of warnings, each warning event carries
// the same fields as the matching entry of the envelope's warnings array.
func TestTS07_36_WarningEventsMirrorTheEnvelope(t *testing.T) {
	codes := make([]WarnCode, 0, len(warnStages))
	for c := range warnStages {
		codes = append(codes, c)
	}
	severities := []string{"low", "medium", "high"}
	messages := []string{"cut", "  padded  ", "has \"quotes\"", "multi\nline", "unicode é ✓", "100%"}
	rng := rand.New(rand.NewSource(7))

	for iter := 0; iter < 50; iter++ {
		run, buf := warnSinkRun()
		n := rng.Intn(8)
		for i := 0; i < n; i++ {
			run.Warn(codes[rng.Intn(len(codes))], severities[rng.Intn(len(severities))], "%s", messages[rng.Intn(len(messages))])
		}
		env := run.Envelope(ExitOK, nil, nil)
		var evs []map[string]any
		for _, ev := range decodeLines(t, buf.String()) {
			if ev["type"] == "warning" {
				evs = append(evs, ev)
			}
		}
		if len(evs) != len(env.Warnings) {
			t.Fatalf("iter %d: %d warning events, %d envelope warnings", iter, len(evs), len(env.Warnings))
		}
		for i, ev := range evs {
			w := env.Warnings[i]
			if ev["code"] != string(w.Code) || ev["severity"] != w.Severity || ev["stage"] != w.Stage || ev["message"] != w.Message {
				t.Fatalf("iter %d warning %d: event %v does not match envelope entry %+v", iter, i, ev, w)
			}
		}
	}
}
