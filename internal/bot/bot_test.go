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

	// Should deny access when allowed_users is empty
	if b.isAuthorized(123456789, "user1") {
		t.Error("expected isAuthorized to return false when allowed_users is empty, got true")
	}
}

func TestIsAuthorized_ValidUsers(t *testing.T) {
	b := &BotCoordinator{
		cfg: &config.Config{
			AllowedUsers: []string{"123456789", "Alice", "bob_username"},
		},
	}

	// ID match
	if !b.isAuthorized(123456789, "unknown") {
		t.Error("expected user ID 123456789 to be authorized")
	}

	// Username match (case insensitive)
	if !b.isAuthorized(999999, "alice") {
		t.Error("expected username 'alice' to be authorized")
	}

	if !b.isAuthorized(999999, "BOB_USERNAME") {
		t.Error("expected username 'BOB_USERNAME' to be authorized")
	}

	// Unauthorized user
	if b.isAuthorized(888888, "charlie") {
		t.Error("expected unauthorized user 'charlie' to return false")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10", 10 * time.Second},
		{"5", 5 * time.Second},
		{"0", 5 * time.Second},   // Default fallback
		{"-3", 5 * time.Second},  // Default fallback
		{"abc", 5 * time.Second}, // Default fallback
		{"", 5 * time.Second},    // Default fallback
	}

	for _, tt := range tests {
		got := parseDuration(tt.input)
		if got != tt.expected {
			t.Errorf("parseDuration(%q) = %v; expected %v", tt.input, got, tt.expected)
		}
	}
}
