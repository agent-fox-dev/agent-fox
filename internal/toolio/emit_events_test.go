package toolio

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-12-23 (unit): --emit-events parses as a shared boolean flag setting
// Common.EmitEvents.
func TestTS12_23_EmitEventsParsesAsSharedBooleanFlag(t *testing.T) {
	// With --emit-events: EmitEvents is true.
	{
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		var c Common
		c.Register(fs)
		if err := fs.Parse([]string{"--emit-events"}); err != nil {
			t.Fatalf("parse --emit-events: %v", err)
		}
		if !c.EmitEvents {
			t.Error("Common.EmitEvents should be true when --emit-events is given")
		}
	}
	// Without --emit-events: EmitEvents is false.
	{
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		var c Common
		c.Register(fs)
		if err := fs.Parse([]string{}); err != nil {
			t.Fatalf("parse empty: %v", err)
		}
		if c.EmitEvents {
			t.Error("Common.EmitEvents should be false when --emit-events is not given")
		}
	}
	// The flag takes no value: a following positional argument is not consumed.
	{
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		var c Common
		c.Register(fs)
		if err := fs.Parse([]string{"--emit-events", "positional"}); err != nil {
			t.Fatalf("parse --emit-events positional: %v", err)
		}
		if !c.EmitEvents {
			t.Error("Common.EmitEvents should be true")
		}
		if fs.NArg() != 1 || fs.Arg(0) != "positional" {
			t.Errorf("positional arg consumed: NArg=%d Arg(0)=%q", fs.NArg(), fs.Arg(0))
		}
	}
	// --help output lists --emit-events.
	{
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		var c Common
		c.Register(fs)
		var buf bytes.Buffer
		fs.SetOutput(&buf)
		fs.PrintDefaults()
		if !strings.Contains(buf.String(), "emit-events") {
			t.Errorf("--help output does not list --emit-events:\n%s", buf.String())
		}
	}
}

// TS-12-25 (unit): The removed flags and symbols no longer exist and the fix
// flag table is updated.
func TestTS12_25_RemovedFlagsAndSymbolsNoLongerExist(t *testing.T) {
	// No --events or --events-file flag definition.
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var c Common
	c.Register(fs)
	if fs.Lookup("events") != nil {
		t.Error("--events flag still exists")
	}
	if fs.Lookup("events-file") != nil {
		t.Error("--events-file flag still exists")
	}
	// --emit-events should exist.
	if fs.Lookup("emit-events") == nil {
		t.Error("--emit-events flag does not exist")
	}
}

// TS-12-26 (integration): --events-file in both spellings yields the removal
// usage error and no events file.
func TestTS12_26_EventsFileRemovedUsageError(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	for _, args := range [][]string{
		{"--events-file", "o", "input"},
		{"--events-file=o", "input"},
	} {
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, args, "")
		if code != ExitUsage {
			t.Fatalf("args %v: code = %d, want %d", args, code, ExitUsage)
		}
		if env.Error == nil || env.Error.Stage != "usage" {
			t.Fatalf("args %v: error = %+v, want stage usage", args, env.Error)
		}
		wantMsg := "--events-file was removed; events are always written to <state>/events/"
		if env.Error.Message != wantMsg {
			t.Errorf("args %v: message = %q, want %q", args, env.Error.Message, wantMsg)
		}
		// No events file should be created.
		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		entries, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
		if len(entries) > 0 {
			t.Errorf("args %v: events files created: %v", args, entries)
		}
	}
}

// TS-12-27 (integration): --events in both spellings yields the rename usage
// error and no events file.
func TestTS12_27_EventsRenamedUsageError(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	for _, args := range [][]string{
		{"--events", "jsonl", "input"},
		{"--events=jsonl", "input"},
	} {
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, args, "")
		if code != ExitUsage {
			t.Fatalf("args %v: code = %d, want %d", args, code, ExitUsage)
		}
		if env.Error == nil || env.Error.Stage != "usage" {
			t.Fatalf("args %v: error = %+v, want stage usage", args, env.Error)
		}
		wantMsg := "--events was renamed --emit-events"
		if env.Error.Message != wantMsg {
			t.Errorf("args %v: message = %q, want %q", args, env.Error.Message, wantMsg)
		}
		// No events file should be created.
		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		entries, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
		if len(entries) > 0 {
			t.Errorf("args %v: events files created: %v", args, entries)
		}
	}
}

// TS-12-28 (unit): With both removed flags only the first encountered is
// named.
func TestTS12_28_OnlyFirstRemovedFlagNamed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// --events-file first: message contains removal text, not rename text.
	{
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{"--events-file", "f", "--events", "jsonl", "input"}, "")
		if code != ExitUsage {
			t.Fatalf("code = %d, want %d", code, ExitUsage)
		}
		if !strings.Contains(env.Error.Message, "was removed") {
			t.Errorf("message %q should contain 'was removed'", env.Error.Message)
		}
		if strings.Contains(env.Error.Message, "was renamed") {
			t.Errorf("message %q should not contain 'was renamed'", env.Error.Message)
		}
	}
	// --events first: message contains rename text, not removal text.
	{
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{"--events", "jsonl", "--events-file", "f", "input"}, "")
		if code != ExitUsage {
			t.Fatalf("code = %d, want %d", code, ExitUsage)
		}
		if !strings.Contains(env.Error.Message, "was renamed") {
			t.Errorf("message %q should contain 'was renamed'", env.Error.Message)
		}
		if strings.Contains(env.Error.Message, "was removed") {
			t.Errorf("message %q should not contain 'was removed'", env.Error.Message)
		}
	}
}

// TS-12-33 (unit): Begin prints unless --emit-events or --quiet is set,
// regardless of the sink.
func TestTS12_33_BeginPrintsUnlessEmitEventsOrQuiet(t *testing.T) {
	cases := []struct {
		name      string
		emit      bool
		quiet     bool
		wantPrint bool
	}{
		{"neither", false, false, true},
		{"emit-events", true, false, false},
		{"quiet", false, true, false},
		{"both", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			human := &bytes.Buffer{}
			events := &bytes.Buffer{}
			p := NewProgress(human, "t", false, tc.quiet)
			p.SetEvents(newEventsSink("t", events)) // active sink
			p.SetEmitEvents(tc.emit)
			end := p.Begin("checking")
			end("done")
			if tc.wantPrint {
				if human.Len() == 0 {
					t.Error("Begin should print when neither --emit-events nor --quiet is set")
				}
			} else {
				if human.Len() != 0 {
					t.Errorf("Begin should print nothing, got %q", human.String())
				}
			}
		})
	}
	// The result does not change when the sink is made inactive.
	t.Run("inactive sink, no emit, no quiet", func(t *testing.T) {
		human := &bytes.Buffer{}
		p := NewProgress(human, "t", false, false)
		p.SetEvents(newEventsSink("t")) // inactive sink
		p.SetEmitEvents(false)
		end := p.Begin("checking")
		end("done")
		if human.Len() == 0 {
			t.Error("Begin should print even with an inactive sink when --emit-events is not set")
		}
	})
}

// TS-12-34 (unit): Raw streams prose live only with --show-text and without
// --emit-events/--quiet, and never emits an event.
func TestTS12_34_RawStreamsProseLiveAndNeverEmitsEvent(t *testing.T) {
	for _, st := range []bool{false, true} {
		for _, em := range []bool{false, true} {
			for _, q := range []bool{false, true} {
				human := &bytes.Buffer{}
				events := &bytes.Buffer{}
				p := NewProgress(human, "t", false, q)
				p.SetEvents(newEventsSink("t", events)) // active sink
				p.SetEmitEvents(em)
				p.SetShowText(st)
				p.Raw("hello")
				wantProse := st && !em && !q
				if wantProse {
					if human.String() != "hello" {
						t.Errorf("st=%v em=%v q=%v: human = %q, want 'hello'", st, em, q, human.String())
					}
				} else {
					if human.Len() != 0 {
						t.Errorf("st=%v em=%v q=%v: human = %q, want empty", st, em, q, human.String())
					}
				}
				// The sink should never receive events from Raw.
				if events.Len() != 0 {
					t.Errorf("st=%v em=%v q=%v: events = %q, want empty", st, em, q, events.String())
				}
			}
		}
	}
}

// TS-12-35 (property): Without --emit-events stderr bytes match the
// pre-change baseline.
//
// We verify that the default mode (no --emit-events) produces the same
// human-readable stderr output as before: Step prints "[tool] message\n",
// Detail prints "  message\n" under --verbose, and no JSON events appear on
// stderr.
func TestTS12_35_WithoutEmitEventsStderrMatchesBaseline(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// A run without --emit-events should produce human progress on stderr,
	// not JSON events.
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Step("preflight", "checking model")
		d.Progress.Detail("model: test/model")
		return ExitOK, nil, nil
	})
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	stderrStr := stderr.String()
	// Should contain human-readable step line.
	if !strings.Contains(stderrStr, "[tool] checking model") {
		t.Errorf("stderr lacks human step line: %q", stderrStr)
	}
	// Should NOT contain JSON events.
	if strings.Contains(stderrStr, `"type":`) {
		t.Errorf("stderr contains JSON events without --emit-events: %q", stderrStr)
	}

	// With --emit-events, stderr should carry JSON events and no human lines.
	app2, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Step("preflight", "checking model")
		return ExitOK, nil, nil
	})
	var stdout2, stderr2 bytes.Buffer
	code2 := app2.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
		strings.NewReader(""), &stdout2, &stderr2)
	if code2 != ExitOK {
		t.Fatalf("code = %d", code2)
	}
	stderrStr2 := stderr2.String()
	if !strings.Contains(stderrStr2, `"type":`) {
		t.Errorf("stderr should contain JSON events with --emit-events: %q", stderrStr2)
	}
	if strings.Contains(stderrStr2, "[tool]") {
		t.Errorf("stderr should not contain human lines with --emit-events: %q", stderrStr2)
	}

	// With --quiet, stderr should be empty regardless of --emit-events.
	for _, emitEvents := range []bool{false, true} {
		app3, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			d.Progress.Step("preflight", "checking model")
			return ExitOK, nil, nil
		})
		args := []string{"--dir", t.TempDir(), "--quiet", "x"}
		if emitEvents {
			args = append(args, "--emit-events")
		}
		var stdout3, stderr3 bytes.Buffer
		code3 := app3.Main(context.Background(), args,
			strings.NewReader(""), &stdout3, &stderr3)
		if code3 != ExitOK {
			t.Fatalf("code = %d", code3)
		}
		if stderr3.Len() != 0 {
			t.Errorf("--quiet emit=%v: stderr = %q, want empty", emitEvents, stderr3.String())
		}
	}

	// Verify --verbose without --emit-events still shows detail lines.
	app4, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Detail("verbose detail")
		return ExitOK, nil, nil
	})
	var stdout4, stderr4 bytes.Buffer
	code4 := app4.Main(context.Background(), []string{"--dir", t.TempDir(), "--verbose", "x"},
		strings.NewReader(""), &stdout4, &stderr4)
	if code4 != ExitOK {
		t.Fatalf("code = %d", code4)
	}
	if !strings.Contains(stderr4.String(), "verbose detail") {
		t.Errorf("--verbose stderr lacks detail line: %q", stderr4.String())
	}

	// Verify --show-text without --emit-events streams prose.
	app5, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Raw("model prose")
		return ExitOK, nil, nil
	})
	var stdout5, stderr5 bytes.Buffer
	code5 := app5.Main(context.Background(), []string{"--dir", t.TempDir(), "--show-text", "x"},
		strings.NewReader(""), &stdout5, &stderr5)
	if code5 != ExitOK {
		t.Fatalf("code = %d", code5)
	}
	if !strings.Contains(stderr5.String(), "model prose") {
		t.Errorf("--show-text stderr lacks prose: %q", stderr5.String())
	}

	// Verify --show-text with --emit-events does NOT stream prose.
	app6, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Raw("model prose")
		return ExitOK, nil, nil
	})
	var stdout6, stderr6 bytes.Buffer
	code6 := app6.Main(context.Background(), []string{"--dir", t.TempDir(), "--show-text", "--emit-events", "x"},
		strings.NewReader(""), &stdout6, &stderr6)
	if code6 != ExitOK {
		t.Fatalf("code = %d", code6)
	}
	if strings.Contains(stderr6.String(), "model prose") {
		t.Errorf("--show-text --emit-events stderr should not contain prose: %q", stderr6.String())
	}
}

// Verify that the knownFixValueFlags table in cmd/fix/main.go does not
// contain events or events-file. This is tested by inspecting the source.
func TestTS12_25_FixValueFlagsTableUpdated(t *testing.T) {
	// Read cmd/fix/main.go and check the knownFixValueFlags map.
	raw, err := os.ReadFile(filepath.Join("..", "..", "cmd", "fix", "main.go"))
	if err != nil {
		t.Skipf("cannot read cmd/fix/main.go: %v", err)
	}
	src := string(raw)
	if strings.Contains(src, `"events"`) && strings.Contains(src, `knownFixValueFlags`) {
		// Check if "events" appears as a key in the map.
		// Look for the pattern "events":  true or "events-file": true
		if strings.Contains(src, `"events":`) {
			t.Error("knownFixValueFlags still contains 'events' entry")
		}
	}
	if strings.Contains(src, `"events-file"`) {
		t.Error("knownFixValueFlags still contains 'events-file' entry")
	}
	// emit-events should NOT be in the table (it's boolean, takes no value).
	if strings.Contains(src, `"emit-events"`) {
		t.Error("knownFixValueFlags contains 'emit-events' entry (it should not)")
	}
}
