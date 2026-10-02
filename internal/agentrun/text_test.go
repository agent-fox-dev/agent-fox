package agentrun

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// TS-12-37 (unit): A turn's text blocks are accumulated and joined with a
// newline, with no trailing newline, regardless of ShowText.
func TestTS12_37_TextBlocksAccumulatedJoinedNewline(t *testing.T) {
	// A turn with text blocks "a", "b" (one TextEndEvent), then "c" (another
	// TextEndEvent), then TurnEndEvent. The faux provider sends
	// TextDeltaEvent for each part of a TextBlock, then TextEndEvent.
	// We use two text blocks in one turn to get the "\n" join.
	for _, showText := range []bool{false, true} {
		t.Run("", func(t *testing.T) {
			// Script: one turn with two text blocks, then submit.
			// faux.Turn with two TextBlocks produces:
			//   TextDelta("ab"), TextEnd("ab"), TextDelta("c"), TextEnd("c"), TurnEnd
			p := faux.New(
				faux.Turn{
					Blocks: []core.ContentBlock{
						faux.FauxText("ab"),
						faux.FauxText("c"),
					},
					StopReason: core.StopReasonToolUse,
				},
				toolCallTurn("c1", "submit", map[string]any{"value": "done"}),
			)
			obs := &recObserver{verbose: true}
			cfg := fauxConfig(p, newWorkspace(t))
			cfg.Observer = obs
			cfg.ShowText = showText
			var got string
			var calls int
			r, err := NewRunner(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Run(context.Background(), submitPhase("ph", "", &got, &calls)); err != nil {
				t.Fatalf("Run: %v", err)
			}

			// Find the text call for turn 1.
			if len(obs.texts) == 0 {
				t.Fatal("no text event emitted")
			}
			tc := obs.texts[0]
			// The two text blocks should be joined with "\n".
			want := "ab\nc"
			if tc.text != want {
				t.Errorf("text = %q, want %q (showText=%v)", tc.text, want, showText)
			}
			// No trailing newline.
			if tc.text[len(tc.text)-1] == '\n' {
				t.Errorf("text has trailing newline: %q", tc.text)
			}
		})
	}
}

// TS-12-38 (unit): The text event is emitted directly before its turn event
// with the same turn number.
func TestTS12_38_TextEventBeforeTurnEventSameTurnNumber(t *testing.T) {
	// Two turns, each with prose and a tool call (so the loop continues).
	p := faux.New(
		textAndToolTurn("first", "c1", "submit", map[string]any{"value": ""}),
		textAndToolTurn("second", "c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{verbose: true}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	var got string
	var calls int
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), submitPhase("ph", "", &got, &calls)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Check that each text call is immediately followed by a turn call with
	// the same turn number.
	for i, entry := range obs.log {
		if entry != "text" {
			continue
		}
		if i+1 >= len(obs.log) {
			t.Fatalf("text at position %d is the last entry", i)
		}
		if obs.log[i+1] != "turn" {
			t.Errorf("text at position %d is followed by %q, want turn", i, obs.log[i+1])
		}
	}

	// Check turn numbers match.
	if len(obs.texts) < 2 {
		t.Fatalf("got %d text events, want at least 2", len(obs.texts))
	}
	if obs.texts[0].turn != 1 {
		t.Errorf("first text turn = %d, want 1", obs.texts[0].turn)
	}
	if obs.texts[1].turn != 2 {
		t.Errorf("second text turn = %d, want 2", obs.texts[1].turn)
	}
	// Verify the turn events have the same numbers.
	if len(obs.turns) < 2 {
		t.Fatalf("got %d turn events, want at least 2", len(obs.turns))
	}
	if obs.turns[0].turn != 1 {
		t.Errorf("first turn number = %d, want 1", obs.turns[0].turn)
	}
	if obs.turns[1].turn != 2 {
		t.Errorf("second turn number = %d, want 2", obs.turns[1].turn)
	}
}

// TS-12-39 (unit): A turn with no prose emits no text event.
func TestTS12_39_TurnWithNoProseNoTextEvent(t *testing.T) {
	// A turn with only a tool call, no text.
	p := faux.New(
		toolCallTurn("c1", "submit", map[string]any{"value": ""}),
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{verbose: true}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	var got string
	var calls int
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), submitPhase("ph", "", &got, &calls)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(obs.texts) != 0 {
		t.Errorf("got %d text events, want 0: %+v", len(obs.texts), obs.texts)
	}
	if len(obs.turns) < 1 {
		t.Fatal("no turn events emitted")
	}
}

// TS-12-40 (unit): Prose buffered when a phase ends without a turn end is
// flushed once before phase_end with turn = completed+1.
func TestTS12_40_BufferedProseFlushBeforePhaseEnd(t *testing.T) {
	// Part 1: Test the textBuffer flush directly. This verifies the
	// mechanism that flushes buffered prose before phase_end when a turn
	// was interrupted (no TurnEndEvent).
	t.Run("direct_flush", func(t *testing.T) {
		obs := &recObserver{}
		var tb textBuffer
		// Simulate one completed turn.
		tb.startBlock()
		tb.appendDelta("first")
		tb.flush(obs, "ph", 1)
		if len(obs.texts) != 1 || obs.texts[0].text != "first" {
			t.Fatalf("first flush: %+v", obs.texts)
		}

		// Simulate prose in a second turn that is interrupted.
		tb.startBlock()
		tb.appendDelta("interrupted")
		// No TurnEndEvent — flush at phase end with turn = completed+1.
		tb.flush(obs, "ph", 2)
		if len(obs.texts) != 2 {
			t.Fatalf("got %d text events, want 2: %+v", len(obs.texts), obs.texts)
		}
		if obs.texts[1].turn != 2 {
			t.Errorf("flushed text turn = %d, want 2", obs.texts[1].turn)
		}
		if obs.texts[1].text != "interrupted" {
			t.Errorf("flushed text = %q, want 'interrupted'", obs.texts[1].text)
		}

		// A second flush must not emit again.
		tb.flush(obs, "ph", 3)
		if len(obs.texts) != 2 {
			t.Errorf("double flush emitted %d texts, want 2", len(obs.texts))
		}
	})

	// Part 2: End-to-end test. A turn with prose and a tool call, then a
	// text-only turn (the model answers in prose, ending the run). The
	// loop fires TurnEndEvent for both, so the text is flushed at
	// TurnEndEvent. The post-loop flush finds an empty buffer. We verify
	// that the text event precedes phase_end and is not emitted twice.
	t.Run("end_to_end", func(t *testing.T) {
		p := faux.New(
			textAndToolTurn("first", "c1", "submit", map[string]any{"value": ""}),
			// The model answers in prose on the second turn.
			textTurnWithText("second"),
		)
		obs := &recObserver{verbose: true}
		cfg := fauxConfig(p, newWorkspace(t))
		cfg.Observer = obs
		var got string
		var calls int
		r, err := NewRunner(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r.Run(context.Background(), submitPhase("ph", "", &got, &calls))

		// Both turns should have text events.
		if len(obs.texts) < 2 {
			t.Fatalf("got %d text events, want at least 2: %+v\nlog: %v", len(obs.texts), obs.texts, obs.log)
		}

		// The second text event should have turn = 2.
		if obs.texts[1].turn != 2 {
			t.Errorf("flushed text turn = %d, want 2", obs.texts[1].turn)
		}
		if obs.texts[1].text != "second" {
			t.Errorf("flushed text = %q, want 'second'", obs.texts[1].text)
		}

		// The text event should precede phase_end in the log.
		lastTextIdx := -1
		phaseEndIdx := -1
		for i, entry := range obs.log {
			if entry == "text" {
				lastTextIdx = i
			}
			if entry == "phase_end" {
				phaseEndIdx = i
			}
		}
		if lastTextIdx == -1 {
			t.Fatal("no text in log")
		}
		if phaseEndIdx == -1 {
			t.Fatal("no phase_end in log")
		}
		if lastTextIdx >= phaseEndIdx {
			t.Errorf("text at %d is not before phase_end at %d; log: %v", lastTextIdx, phaseEndIdx, obs.log)
		}

		// Exactly one text event with turn=2 (not emitted twice).
		count := 0
		for _, tc := range obs.texts {
			if tc.turn == 2 {
				count++
			}
		}
		if count != 1 {
			t.Errorf("turn=2 text events = %d, want exactly 1", count)
		}
	})
}

// textTurnWithText creates a turn that has text but no tool call.
// The model answers in prose and the turn ends with StopReasonStop.
func textTurnWithText(s string) faux.Turn {
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxText(s)},
		StopReason: core.StopReasonStop,
	}
}

// Helper to create a turn with text and a tool call.
func textAndToolTurn(text, id, toolName string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks: []core.ContentBlock{
			faux.FauxText(text),
			faux.FauxToolCall(id, toolName, string(raw)),
		},
		StopReason: core.StopReasonToolUse,
	}
}
