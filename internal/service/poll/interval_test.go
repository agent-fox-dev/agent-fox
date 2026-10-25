package poll

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	for value, want := range map[string]interval{
		"": {every: 5 * time.Minute}, "  ": {every: 5 * time.Minute},
		"1m": {every: time.Minute}, "2m": {every: 2 * time.Minute}, " 1h30m ": {every: 90 * time.Minute}, "24h": {every: 24 * time.Hour},
		"0": {every: 5 * time.Minute, off: true}, "0s": {every: 5 * time.Minute, off: true}, "0m": {every: 5 * time.Minute, off: true},
		"30s": {every: 5 * time.Minute, invalid: true}, "59s": {every: 5 * time.Minute, invalid: true},
		"25h": {every: 5 * time.Minute, invalid: true}, "24h1s": {every: 5 * time.Minute, invalid: true},
		"-1m": {every: 5 * time.Minute, invalid: true}, "abc": {every: 5 * time.Minute, invalid: true}, "1x": {every: 5 * time.Minute, invalid: true},
	} {
		if got := parseInterval(value); got != want {
			t.Errorf("parseInterval(%q) = %+v, want %+v", value, got, want)
		}
	}
}
