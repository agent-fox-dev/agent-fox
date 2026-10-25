package toolio

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestEventStream_AreWrittenAsTheyHappenFromRunStartToRunEnd(t *testing.T) {
	dir := t.TempDir()
	var sunk []string
	var whileRunning [2]string // events.jsonl and the sink after the step
	eventsPath, fileFirst := "", true
	app := newApp()
	app.EventSink = func(line []byte) {
		sunk = append(sunk, string(line)+"\n")
		if eventsPath != "" { // a line is in the file before the sink has it
			data, _ := os.ReadFile(eventsPath)
			fileFirst = fileFirst && strings.HasSuffix(string(data), string(line)+"\n")
		}
	}
	app.Register(Command{Path: "X", UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
		eventsPath = filepath.Join(r.RunDir.Path, "events.jsonl")
		r.Warn("first_warning", "one")
		r.Emit("step", map[string]any{"n": 1})
		data, _ := os.ReadFile(eventsPath)
		whileRunning = [2]string{string(data), strings.Join(sunk, "")}
		r.Warn("second_warning", "two")
		r.Emit("heartbeat", nil)
		r.Emit("bad", make(chan int))
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})

	env := run(t, app, "X", "--dir", dir).env(t)

	path := filepath.Join(agentFoxDir(dir), "run", env["run_id"].(string), "events.jsonl")
	file := testutil.ReadFile(t, path)
	events := testutil.JSONLines(t, path)
	if got := eventTypes(events); !slices.Equal(got, []string{"run_start", "warning", "step", "warning", "heartbeat", "bad", "run_end"}) {
		t.Fatalf("event types %v", got)
	}
	for i, seen := range whileRunning {
		if !strings.HasPrefix(file, seen) || strings.Count(seen, "\n") != 3 {
			t.Errorf("copy %d held, while the command ran, %q; want run_start, the warning and the step", i, seen)
		}
	}
	stamp := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	for i, e := range events {
		if e["seq"] != float64(i+1) || e["run_id"] != env["run_id"] || !stamp.MatchString(e["ts"].(string)) {
			t.Errorf("event %d = %v, want seq %d, the run id and a time stamp", i, e, i+1)
		}
	}
	for i, want := range map[int]map[string]any{
		0: {"tool": "X", "input_kind": "none", "argv": []any{"af", "X", "--dir", dir}},
		1: {"code": "first_warning", "message": "one"},
		6: {"status": "done", "exit_code": 0.0, "duration_ms": events[6]["data"].(map[string]any)["duration_ms"]},
	} {
		if !reflect.DeepEqual(events[i]["data"], want) {
			t.Errorf("event %d data %v, want %v", i, events[i]["data"], want)
		}
	}
	if msg, _ := at(events[5], "data", "error").(string); !strings.Contains(msg, "cannot be encoded") {
		t.Errorf("the data of an event that cannot be encoded is %v", events[5]["data"])
	}
	if got := strings.Join(sunk, ""); got != file || !fileFirst {
		t.Errorf("the sink got\n%s\nand events.jsonl holds\n%s", got, file)
	}
}

func TestEventStream_GoToTheSinkOfACommandWithoutARunDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newApp()
	app.Register(Command{Path: "X", Input: InputRequired, Run: func(r *Run) (Outcome, error) {
		r.Emit("step", nil)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})
	events := sinkEvents(t, app)

	run(t, app, "X", "text")

	if !slices.Equal(eventTypes(*events), []string{"run_start", "step", "run_end"}) || (*events)[0]["run_id"] != nil || at((*events)[0], "data", "input_kind") != "text" {
		t.Errorf("events %v, want run_start with no run id and the input kind, the step and run_end", *events)
	}
}

func TestEventStream_ARefusedInvocationHasRunStartAndRunEnd(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"X", "a", "b"},
		{"X", "--bogus"},
		{"X", "--dir", filepath.Join(dir, "missing")},
		{"X", "--timeout", "-1s"},
		{"X", "   ", "--dir", dir},
	} {
		var rec recorder
		app := newApp()
		app.Register(rec.command(Command{Path: "X", UsesRunDir: true, Timeout: true, Input: InputOptional}))
		events := sinkEvents(t, app)

		r := run(t, app, args...)

		if r.code != 2 || rec.ran != 0 || !slices.Equal(eventTypes(*events), []string{"run_start", "run_end"}) ||
			at((*events)[1], "data", "status") != "usage" || at((*events)[1], "data", "exit_code") != 2.0 {
			t.Errorf("%q: exit %d, ran %d, events %v; want usage between run_start and run_end", args, r.code, rec.ran, *events)
		}
	}
}

func TestEventStream_AFileThatCannotBeWrittenIsWarnedOnce(t *testing.T) {
	var warned []string
	stream := newEventStream(time.Now, func(code, _ string) { warned = append(warned, code) })
	stream.addFile(filepath.Join(t.TempDir(), "missing", "events.jsonl"))
	stream.addFile(filepath.Join(t.TempDir(), "missing", "again.jsonl"))
	if stream.active() {
		t.Error("a stream with no file it can write is active")
	}

	stream.begin("X", []string{"af", "X"}, KindNone, []Warning{{"w", "m"}})
	for i := range 3 {
		stream.emit("step", i)
	}

	if !slices.Equal(warned, []string{WarnEventsUnwritable}) {
		t.Errorf("warnings %v, want one %s", warned, WarnEventsUnwritable)
	}

	// A file that fails once it is open is given up the same way.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	warned = nil
	broken := newEventStream(time.Now, func(code, _ string) { warned = append(warned, code) })
	broken.sinks = append(broken.sinks, &eventSink{path: "a pipe", f: w})
	broken.begin("X", nil, KindNone, nil)
	broken.emit("step", nil)
	if !slices.Equal(warned, []string{WarnEventsUnwritable}) || broken.active() {
		t.Errorf("warnings %v, active %v; want one %s and the file given up", warned, broken.active(), WarnEventsUnwritable)
	}
}

func TestEventStream_AfterTheRunEndedAreDropped(t *testing.T) {
	var late *Run
	app := newApp()
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		late = r
		return Outcome{Status: StatusDone}, nil
	}})
	events := sinkEvents(t, app)
	app.Main(context.Background(), []string{"af", "X"}, strings.NewReader(""), io.Discard, io.Discard)
	before := len(*events)

	late.Emit("step", nil)
	late.Warn("late_warning", "after the end")

	if len(*events) != before {
		t.Errorf("%d events were written after run_end", len(*events)-before)
	}
}
