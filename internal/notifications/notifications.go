package notifications

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	wtsapi32                         = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSSendMessageW              = wtsapi32.NewProc("WTSSendMessageW")
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procWTSGetActiveConsoleSessionId = kernel32.NewProc("WTSGetActiveConsoleSessionId")
)

const (
	WTS_CURRENT_SERVER_HANDLE = 0
	MB_OK                     = 0x00000000
	MB_ICONINFORMATION        = 0x00000040
	MB_SYSTEMMODAL            = 0x00001000
)

// ShowAlert displays a message box to the active logged-in user from Session 0 using WTSSendMessageW.
func ShowAlert(title, message string) error {
	sessionID, _, _ := procWTSGetActiveConsoleSessionId.Call()
	if sessionID == 0xFFFFFFFF {
		// No active console session, default to session 1
		sessionID = 1
	}

	titleU16, err := syscall.UTF16FromString(title)
	if err != nil {
		return err
	}
	messageU16, err := syscall.UTF16FromString(message)
	if err != nil {
		return err
	}

	titlePtr := &titleU16[0]
	messagePtr := &messageU16[0]

	titleLen := len(titleU16) * 2
	messageLen := len(messageU16) * 2
	style := MB_OK | MB_ICONINFORMATION | MB_SYSTEMMODAL
	timeout := 0 // Wait indefinitely
	var response uint32

	ret, _, err := procWTSSendMessageW.Call(
		WTS_CURRENT_SERVER_HANDLE,
		sessionID,
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(titleLen),
		uintptr(unsafe.Pointer(messagePtr)),
		uintptr(messageLen),
		uintptr(style),
		uintptr(timeout),
		uintptr(unsafe.Pointer(&response)),
		1, // Wait for user to dismiss
	)

	if ret == 0 {
		return fmt.Errorf("failed to send message box: %v", err)
	}

	return nil
}

// ShowToastLocal displays a native Windows toast notification. This must be executed in the user's interactive session.
func ShowToastLocal(title, message string) error {
	escapedTitle := strings.ReplaceAll(title, "'", "''")
	escapedMessage := strings.ReplaceAll(message, "'", "''")

	psScript := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$Template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$RawXml = [xml]$Template.GetXml()
$RawXml.toast.visual.binding.text[0].AppendChild($RawXml.CreateTextNode('%s')) | Out-Null
$RawXml.toast.visual.binding.text[1].AppendChild($RawXml.CreateTextNode('%s')) | Out-Null
$AppId = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($AppId).Show((New-Object Windows.UI.Notifications.ToastNotification($RawXml)))
`, escapedTitle, escapedMessage)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
