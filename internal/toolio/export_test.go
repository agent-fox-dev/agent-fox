package toolio

import (
	"testing"
	"time"
)

// ShortenHeartbeat narrows the heartbeat window for the rest of the test, so
// a smoke test of the whole shell sees a heartbeat without waiting fifteen
// seconds. It must be called before App.Main builds the sink, and the test
// must not run in parallel with another that builds one.
func ShortenHeartbeat(t *testing.T, interval, idle time.Duration) {
	t.Helper()
	oldInterval, oldIdle := heartbeatInterval, heartbeatIdle
	heartbeatInterval, heartbeatIdle = interval, idle
	t.Cleanup(func() { heartbeatInterval, heartbeatIdle = oldInterval, oldIdle })
}
