package config

import (
	"testing"
	"time"
)

func TestPMSyncInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":         time.Hour, // unset: preventive maintenance stays on
		"1h":       time.Hour,
		"30m":      30 * time.Minute,
		"5s":       time.Minute, // floored; a tighter loop only hammers the database
		"0":        0,
		"off":      0,
		"FALSE":    0,
		"nonsense": time.Hour, // a typo must not silently switch PM off
		"-1h":      time.Hour,
	}
	for in, want := range cases {
		if got := pmSyncInterval(in); got != want {
			t.Errorf("pmSyncInterval(%q) = %s, want %s", in, got, want)
		}
	}
}
