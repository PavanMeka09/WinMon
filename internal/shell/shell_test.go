package shell

import (
	"testing"
	"time"
)

func TestExecuteCommand(t *testing.T) {
	// Execute a simple echo command to verify it runs directly
	out, err := ExecuteCommand("echo Hello", 2*time.Second)
	if err != nil {
		t.Fatalf("expected no error executing echo command, got: %v", err)
	}
	if !contains(out, "Hello") {
		t.Errorf("expected output to contain 'Hello', got: %q", out)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
