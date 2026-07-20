package shell

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// ExecuteCommand executes a command line string securely using cmd.exe /c
func ExecuteCommand(cmdLine string, timeout time.Duration) (string, error) {
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
