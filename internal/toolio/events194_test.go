package toolio

import (
	"strings"
	"testing"
	"time"
)

// Issue #194 (1): a turn event carries the cache tokens and the context the
// turn sent, and omits them when the provider reports none.
func TestTurnEventCarriesCacheTokens(t *testing.T) {
	buf := &syncBuffer{}
	s := newEventsSink("impl", buf)
	s.Emit(newTurnEvent("implement", 1, 0.11, 3, 249, 98000, 1200))
	s.Emit(newTurnEvent("implement", 2, 0.01, 10, 20, 0, 0))
	turns := eventsOfType(t, buf, "turn")
	if turns[0]["cache_read_tokens"] != float64(98000) || turns[0]["cache_write_tokens"] != float64(1200) ||
		turns[0]["context_tokens"] != float64(99203) {
		t.Errorf("turn 1 = %v", turns[0])
	}
	for _, k := range []string{"cache_read_tokens", "cache_write_tokens"} {
		if _, ok := turns[1][k]; ok {
			t.Errorf("turn 2 carries %s with no cache use: %v", k, turns[1])
		}
	}
	if turns[1]["context_tokens"] != float64(10) {
		t.Errorf("turn 2 context_tokens = %v", turns[1]["context_tokens"])
	}
}

// Issue #194 (2, 3): a heartbeat before any step or phase says preflight, and
// the events file gets one only after a minute of its own silence, while the
// live stream keeps the fifteen-second window.
func TestHeartbeatsArePreflightAndSlowerInTheFile(t *testing.T) {
	clock := newFakeClock()
	file, live := &syncBuffer{}, &syncBuffer{}
	s := newEventsSink("impl", file, live)
	s.now = clock.Now
	s.newTicker = clock.newTicker
	s.slowHeartbeats(file, 60*time.Second)
	s.StartHeartbeat(func() float64 { return 0 })
	t.Cleanup(s.StopHeartbeat)
	clock.Advance(20 * time.Second)

	beats := eventsOfType(t, live, "heartbeat")
	if len(beats) == 0 || beats[0]["stage"] != "preflight" {
		t.Errorf("live heartbeats = %v, want stage preflight", beats)
	}
	if strings.Contains(file.String(), `"heartbeat"`) {
		t.Errorf("the file has a heartbeat after 20s of silence:\n%s", file.String())
	}
	clock.Advance(45 * time.Second)
	if got := len(eventsOfType(t, file, "heartbeat")); got != 1 {
		t.Errorf("the file has %d heartbeat(s) after 65s of silence, want 1", got)
	}
	if got := len(eventsOfType(t, live, "heartbeat")); got < 4 {
		t.Errorf("the live stream has %d heartbeat(s) after 65s, want one per 15s", got)
	}
}
