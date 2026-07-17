package service

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

var (
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	procDuplicateTokenEx     = advapi32.NewProc("DuplicateTokenEx")
	procCreateProcessAsUserW = advapi32.NewProc("CreateProcessAsUserW")

	wtsapi32                         = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQueryUserToken            = wtsapi32.NewProc("WTSQueryUserToken")
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procWTSGetActiveConsoleSessionId = kernel32.NewProc("WTSGetActiveConsoleSessionId")
)

type STARTUPINFO struct {
	Cb            uint32
	Reserved      *uint16
	Desktop       *uint16
	Title         *uint16
	X             uint32
	Y             uint32
	XSize         uint32
	YSize         uint32
	XCountChars   uint32
	YCountChars   uint32
	FillAttribute uint32
	Flags         uint32
	ShowCmd       uint16
	Reserved2     uint16
	Reserved2Ptr  *byte
	StdInput      syscall.Handle
	StdOutput     syscall.Handle
	StdError      syscall.Handle
}

type PROCESS_INFORMATION struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessId uint32
	ThreadId  uint32
}

// WinMonService implements svc.Handler
type WinMonService struct {
	StopChan chan struct{}
}

func (m *WinMonService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(m.StopChan)
				break loop
			default:
				// Ignore unsupported control codes
			}
		}
	}
	changes <- svc.Status{State: svc.Stopped}
	return
}

// RunService runs the bot as a service.
func RunService(serviceName string, stopChan chan struct{}) error {
	return svc.Run(serviceName, &WinMonService{StopChan: stopChan})
}

// RunInUserSession spawns WinMon.exe as a helper inside the active console session.
func RunInUserSession(args string, timeout time.Duration) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	sessionID, _, _ := procWTSGetActiveConsoleSessionId.Call()
	if sessionID == 0xFFFFFFFF {
		return fmt.Errorf("no active console session found")
	}

	var userToken syscall.Handle
	ret, _, err := procWTSQueryUserToken.Call(sessionID, uintptr(unsafe.Pointer(&userToken)))
	if ret == 0 {
		return fmt.Errorf("no user logged in on active console session (WTSQueryUserToken failed: %v)", err)
	}
	defer syscall.CloseHandle(userToken)

	var dupToken syscall.Handle
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(userToken),
		uintptr(0xF01FF), // TOKEN_ALL_ACCESS
		0,
		1, // SecurityIdentification
		1, // TokenPrimary
		uintptr(unsafe.Pointer(&dupToken)),
	)
	if ret == 0 {
		return fmt.Errorf("failed to duplicate user token: %v", err)
	}
	defer syscall.CloseHandle(dupToken)

	var si STARTUPINFO
	si.Cb = uint32(unsafe.Sizeof(si))
	desktopStr := "winsta0\\default"
	desktopUTF16, _ := syscall.UTF16PtrFromString(desktopStr)
	si.Desktop = desktopUTF16

	var pi PROCESS_INFORMATION

	cmdLine := fmt.Sprintf("\"%s\" %s", exePath, args)
	cmdLineUTF16, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return err
	}

	ret, _, err = procCreateProcessAsUserW.Call(
		uintptr(dupToken),
		0,
		uintptr(unsafe.Pointer(cmdLineUTF16)),
		0,
		0,
		0,
		0x08000000, // CREATE_NO_WINDOW
		0,
		0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ret == 0 {
		return fmt.Errorf("CreateProcessAsUserW failed: %v", err)
	}
	defer syscall.CloseHandle(pi.Process)
	defer syscall.CloseHandle(pi.Thread)

	event, err := syscall.WaitForSingleObject(pi.Process, uint32(timeout.Milliseconds()))
	if err != nil {
		return fmt.Errorf("failed waiting for helper process: %v", err)
	}
	if event == syscall.WAIT_TIMEOUT {
		syscall.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("helper process timed out after %s", timeout)
	}

	var exitCode uint32
	syscall.GetExitCodeProcess(pi.Process, &exitCode)
	if exitCode != 0 {
		return fmt.Errorf("helper process exited with non-zero code %d", exitCode)
	}

	return nil
}

// IsRunningAsService returns true if the process is running as a Windows Service.
func IsRunningAsService() bool {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isSvc
}

// Service administration functions
func InstallService(name, displayName, desc string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	s, err := m.CreateService(name, exePath, mgr.Config{
		DisplayName: displayName,
		Description: desc,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	// Update description (mgr doesn't write description directly during CreateService config sometimes)
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `System\CurrentControlSet\Services\`+name, registry.WRITE)
	if err == nil {
		k.SetStringValue("Description", desc)
		k.Close()
	}

	return nil
}

func UninstallService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Delete()
}

func StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Start()
}

func StopService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return err
	}

	// Wait for service to stop
	timeout := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(timeout) {
			return fmt.Errorf("timeout waiting for service to stop")
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return err
		}
	}

	return nil
}
