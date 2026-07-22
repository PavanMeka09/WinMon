package device

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{45 * time.Second, "45s"},
		{5*time.Minute + 12*time.Second, "5m 12s"},
		{3*time.Hour + 20*time.Minute + 5*time.Second, "3h 20m 5s"},
		{2*24*time.Hour + 4*time.Hour + 10*time.Minute + 3*time.Second, "2d 4h 10m 3s"},
		{0, "0s"},
	}

	for _, tt := range tests {
		got := FormatDuration(tt.duration)
		if got != tt.expected {
			t.Errorf("FormatDuration(%v) = %q; expected %q", tt.duration, got, tt.expected)
		}
	}
}
