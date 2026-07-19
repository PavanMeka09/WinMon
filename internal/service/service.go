package service

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

func logDebug(format string, a ...interface{}) {
	f, err := os.OpenFile("C:\\Windows\\Temp\\winmon_service.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] "+format+"\n", append([]interface{}{time.Now().Format("2006-01-02 15:04:05")}, a...)...)
}

// RunInUserSession spawns WinMon.exe as a helper inside the active console session.
func RunInUserSession(args string, timeout time.Duration) error {
	exePath, err := os.Executable()
	if err != nil {
		logDebug("os.Executable failed: %v", err)
		return err
	}
	logDebug("RunInUserSession started. exePath: %s, args: %s", exePath, args)

	sessionID, _, _ := procWTSGetActiveConsoleSessionId.Call()
	logDebug("Active console session ID: %d", sessionID)
	if sessionID == 0xFFFFFFFF {
		logDebug("No active console session found")
		return fmt.Errorf("no active console session found")
	}

	var userToken syscall.Handle
	ret, _, err := procWTSQueryUserToken.Call(sessionID, uintptr(unsafe.Pointer(&userToken)))
	if ret == 0 {
		logDebug("WTSQueryUserToken failed: %v", err)
		return fmt.Errorf("no user logged in on active console session (WTSQueryUserToken failed: %v)", err)
	}
	defer syscall.CloseHandle(userToken)

	var dupToken syscall.Handle
	ret, _, err = procDuplicateTokenEx.Call(
		uintptr(userToken),
		0xF01FF, // TOKEN_ALL_ACCESS
		0,
		1, // SecurityIdentification
		1, // TokenPrimary
		uintptr(unsafe.Pointer(&dupToken)),
	)
	if ret == 0 {
		logDebug("procDuplicateTokenEx failed: %v", err)
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
		logDebug("UTF16PtrFromString failed: %v", err)
		return err
	}

	logDebug("Spawning process: %s", cmdLine)
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
		logDebug("CreateProcessAsUserW failed: %v", err)
		return fmt.Errorf("CreateProcessAsUserW failed: %v", err)
	}
	defer syscall.CloseHandle(pi.Process)
	defer syscall.CloseHandle(pi.Thread)
	logDebug("Process spawned successfully. PID: %d, ThreadID: %d", pi.ProcessId, pi.ThreadId)

	event, err := syscall.WaitForSingleObject(pi.Process, uint32(timeout.Milliseconds()))
	if err != nil {
		logDebug("WaitForSingleObject failed: %v", err)
		return fmt.Errorf("failed waiting for helper process: %v", err)
	}
	if event == syscall.WAIT_TIMEOUT {
		syscall.TerminateProcess(pi.Process, 1)
		logDebug("Helper process timed out")
		return fmt.Errorf("helper process timed out after %s", timeout)
	}

	var exitCode uint32
	syscall.GetExitCodeProcess(pi.Process, &exitCode)
	logDebug("Helper process exited. ExitCode: %d", exitCode)
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

// IsServiceInstalled checks if the service is registered with the Service Control Manager.
func IsServiceInstalled(name string) (bool, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, fmt.Errorf("failed to open SCM: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	utfName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}

	hSvc, err := windows.OpenService(scm, utfName, windows.SERVICE_QUERY_STATUS)
	if err == nil {
		windows.CloseServiceHandle(hSvc)
		return true, nil
	}

	if err == windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return false, nil
	}

	// Any other error (like access denied) means the service is installed.
	return true, nil
}

// IsServiceRunning checks if the service is currently running.
func IsServiceRunning(name string) (bool, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, fmt.Errorf("failed to open SCM: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	utfName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}

	hSvc, err := windows.OpenService(scm, utfName, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, fmt.Errorf("failed to open service with QUERY_STATUS: %w", err)
	}
	defer windows.CloseServiceHandle(hSvc)

	var status windows.SERVICE_STATUS
	err = windows.QueryServiceStatus(hSvc, &status)
	if err != nil {
		return false, fmt.Errorf("failed to query service status: %w", err)
	}

	return status.CurrentState == windows.SERVICE_RUNNING, nil
}

// ElevateProcess re-runs the current executable elevated with administrative arguments.
func ElevateProcess(args string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return err
	}
	argsPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}

	// SW_NORMAL = 1
	err = windows.ShellExecute(0, verbPtr, exePtr, argsPtr, nil, 1)
	if err != nil {
		return err
	}

	return nil
}
