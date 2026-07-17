package display

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
)

const (
	HWND_BROADCAST       = 0xFFFF
	WM_SYSCOMMAND        = 0x0112
	SC_MONITORPOWER      = 0xF170
	SPI_SETDESKWALLPAPER = 20
	SPIF_UPDATEINIFILE   = 0x01
	SPIF_SENDCHANGE      = 0x02
)

// SetBrightness sets the monitor brightness (0-100) using PowerShell CIM methods.
func SetBrightness(brightness int) error {
	if brightness < 0 {
		brightness = 0
	} else if brightness > 100 {
		brightness = 100
	}
	psCmd := fmt.Sprintf(`Get-CimInstance -Namespace root/WMI -ClassName WmiMonitorBrightnessMethods | Invoke-CimMethod -MethodName WmiSetBrightness -Arguments @{ Timeout = 0; Brightness = %d }`, brightness)
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c.Run()
}

// TurnMonitorOff turns off the monitor by sending a SC_MONITORPOWER command to HWND_BROADCAST.
func TurnMonitorOff() error {
	ret, _, err := procSendMessageW.Call(
		HWND_BROADCAST,
		WM_SYSCOMMAND,
		SC_MONITORPOWER,
		2, // 2 = Off, -1 = On, 1 = Low Power
	)
	// SendMessageW returns LRESULT. Zero indicates it was handled, but return code depends on message.
	// Typically on success it returns non-zero, but we check if err is non-nil (0 is syscall.Errno = 0)
	if ret == 0 && err != syscall.Errno(0) {
		return err
	}
	return nil
}

// GetWallpaperPath retrieves the current desktop wallpaper file path from the registry.
func GetWallpaperPath() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\Desktop`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	val, _, err := k.GetStringValue("Wallpaper")
	if err != nil {
		return "", err
	}
	return val, nil
}

// SetWallpaperLocal sets desktop wallpaper. This must be executed in the user's interactive session.
func SetWallpaperLocal(path string) error {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	ret, _, err := procSystemParametersInfoW.Call(
		SPI_SETDESKWALLPAPER,
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		SPIF_UPDATEINIFILE|SPIF_SENDCHANGE,
	)

	if ret == 0 {
		return fmt.Errorf("failed to set wallpaper: %v", err)
	}
	return nil
}
