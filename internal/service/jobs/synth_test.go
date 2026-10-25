package jobs

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestSynthesize_FillsInACategoryAndAMessageAndClipsTheMessageAtACharacter(t *testing.T) {
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	env := synthesize(KindTriage, now, toolio.ErrorInfo{Message: "  "})
	if env.Tool != "triage" || env.Error.Category != toolio.CategoryInternal || env.Error.Message != "the attempt failed (internal)" || env.Summary != env.Error.Message {
		t.Errorf("an empty failure: tool %s, error %+v, summary %q", env.Tool, env.Error, env.Summary)
	}
	if env.StartedAt != "2026-10-22T08:00:00.000Z" || env.FinishedAt != env.StartedAt || env.ExitCode != 1 {
		t.Errorf("times %s %s, exit code %d", env.StartedAt, env.FinishedAt, env.ExitCode)
	}
	for _, long := range []string{strings.Repeat("a", 2000), strings.Repeat("a", 1023) + "é and more", strings.Repeat("€", 400)} {
		msg := synthesize(KindFix, now, toolio.ErrorInfo{Category: toolio.CategoryForge, Message: long}).Error.Message
		if len(msg) > maxMessage || len(msg) < maxMessage-3 || !utf8.ValidString(msg) || !strings.HasPrefix(long, msg) {
			t.Errorf("a message of %d bytes is clipped to %d bytes %q..., want at most %d, at a character", len(long), len(msg), msg[:10], maxMessage)
		}
	}
}

func TestRoundLimit_IsTwoForNothingAndKeptBetweenOneAndTen(t *testing.T) {
	for reported, want := range map[int]int{0: 2, -5: 2, 1: 1, 3: 3, 10: 10, 99: 10} {
		if got := roundLimit(reported); got != want {
			t.Errorf("roundLimit(%d) = %d, want %d", reported, got, want)
		}
	}
}
