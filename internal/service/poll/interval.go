package poll

import (
	"strings"
	"time"
)

// The variables of the workspace that the poll reads, and the bounds of its interval.
const (
	intervalVar = "AF_POLL_INTERVAL"
	prefixVar   = "AF_LABEL_PREFIX"

	// defaultInterval is the interval of a poll with no value or an invalid one, and the wait of a poll that is off.
	defaultInterval = 5 * time.Minute
	minInterval     = time.Minute
	maxInterval     = 24 * time.Hour
)

// interval is what AF_POLL_INTERVAL says.
type interval struct {
	// every is the time between two cycles: the value, or 5 minutes when there is none, it is invalid, or the poll is off.
	every time.Duration
	// off says that the value is a zero duration, which turns the poll off.
	off bool
	// invalid says that the value is none of the allowed ones, and the poll uses 5 minutes.
	invalid bool
}

// parseInterval reads the value of AF_POLL_INTERVAL: nothing is 5 minutes, a zero duration turns the poll off, a duration
// from 1m to 24h is that long, and anything else (a value that is no duration, a negative one, one below a minute or
// above a day) is invalid and means 5 minutes.
func parseInterval(value string) interval {
	v := strings.TrimSpace(value)
	if v == "" {
		return interval{every: defaultInterval}
	}
	d, err := time.ParseDuration(v)
	switch {
	case err != nil || d < 0 || d > maxInterval || (d > 0 && d < minInterval):
		return interval{every: defaultInterval, invalid: true}
	case d == 0:
		return interval{every: defaultInterval, off: true}
	}
	return interval{every: d}
}
