package bot

import (
	"testing"
	"time"

	"winmon/internal/config"
)

func TestIsAuthorized_EmptyAllowedUsers(t *testing.T) {
	b := &BotCoordinator{
		cfg: &config.Config{
			AllowedUsers: []string{},
		},
	}

	if b.isAuthorized(123456789, "user1") {
		t.Error("expected isAuthorized to return false when allowed_users is empty, got true")
	}
}

func TestIsAuthorized_NumericIDsOnly(t *testing.T) {
	b := &BotCoordinator{
		cfg: &config.Config{
			AllowedUsers: []string{"123456789", "Alice", "@999888777"},
		},
	}

	if !b.isAuthorized(123456789, "unknown") {
		t.Error("expected user ID 123456789 to be authorized")
	}

	// Usernames must not authorize, even if listed in config
	if b.isAuthorized(999999, "Alice") {
		t.Error("expected username allowlisting to be disabled")
	}

	if !b.isAuthorized(999888777, "anyone") {
		t.Error("expected numeric ID with @ prefix in config to authorize matching user ID")
	}

	if b.isAuthorized(888888, "charlie") {
		t.Error("expected unauthorized user to return false")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10", 10 * time.Second},
		{"5", 5 * time.Second},
		{"0", 5 * time.Second},
		{"-3", 5 * time.Second},
		{"abc", 5 * time.Second},
		{"", 5 * time.Second},
	}

	for _, tt := range tests {
		got := parseDuration(tt.input)
		if got != tt.expected {
			t.Errorf("parseDuration(%q) = %v; expected %v", tt.input, got, tt.expected)
		}
	}
}
