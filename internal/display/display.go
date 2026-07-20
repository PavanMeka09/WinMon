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
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
)

const (
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
