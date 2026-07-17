package shell

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var ErrCommandBlocked = errors.New("command blocked by allowlist or safety check")

// IsCommandAllowed checks if the given command is safe and configured in the allowlist.
func IsCommandAllowed(cmdLine string, allowlist []string) bool {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		return false
	}

	// Reject dangerous operators
	dangerous := []string{"&", "|", ">", "<", "%", "^", "`"}
	for _, op := range dangerous {
		if strings.Contains(cmdLine, op) {
			return false
		}
	}

	// Double check line breaks (command chaining)
	if strings.Contains(cmdLine, "\n") || strings.Contains(cmdLine, "\r") {
		return false
	}

	// Extract the first word/executable (or words for route print etc)
	lowerCmd := strings.ToLower(cmdLine)
	for _, allowed := range allowlist {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		// Check exact match or matches prefix followed by a space
		if lowerCmd == allowed || strings.HasPrefix(lowerCmd, allowed+" ") {
			return true
		}
	}

	return false
}

// ExecuteCommand executes a command line string securely using cmd.exe /c
func ExecuteCommand(cmdLine string, timeout time.Duration, allowlist []string) (string, error) {
	if !IsCommandAllowed(cmdLine, allowlist) {
		return "", ErrCommandBlocked
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Run command inside cmd.exe /c securely
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", cmdLine)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("%s\n[ERROR: Command execution timed out after %s]", output, timeout), context.DeadlineExceeded
	}

	// Limit output size to prevent bloating Telegram messages
	maxChars := 4000
	if len(output) > maxChars {
		output = output[:maxChars] + "\n... [Output truncated due to size limit]"
	}

	return output, err
}
