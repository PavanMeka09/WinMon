package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// ValidateBinary checks if the file is a valid Windows executable (PE format starting with "MZ").
func ValidateBinary(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	header := make([]byte, 2)
	_, err = file.Read(header)
	if err != nil {
		return err
	}

	if string(header) != "MZ" {
		return fmt.Errorf("invalid Windows executable format (missing MZ header)")
	}
	return nil
}

// UpdateService launches a detached PowerShell updater script and exits.
func UpdateService(tempExePath, botToken string, chatID int64) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(os.TempDir(), "winmon_update.ps1")

	// Escape single quotes and slashes for PowerShell compatibility
	escapedTemp := strings.ReplaceAll(tempExePath, "\\", "\\\\")
	escapedExe := strings.ReplaceAll(exePath, "\\", "\\\\")

	psScript := fmt.Sprintf(`
Start-Sleep -Seconds 2
Stop-Service -Name WinMon -Force
Copy-Item -Path "%s" -Destination "%s" -Force
Start-Service -Name WinMon
$body = @{ chat_id = "%d"; text = "🟢 WinMon service has been updated successfully!" }
Invoke-RestMethod -Uri "https://api.telegram.org/bot%s/sendMessage" -Method Post -Body $body
Remove-Item -Path $MyInvocation.MyCommand.Path -Force
`, escapedTemp, escapedExe, chatID, botToken)

	err = os.WriteFile(scriptPath, []byte(psScript), 0644)
	if err != nil {
		return err
	}

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-File", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008, // DETACHED_PROCESS
	}

	return cmd.Start()
}
