package service

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// SafeClose closes a channel if it isn't already closed.
func SafeClose(ch chan struct{}) {
	defer func() { recover() }()
	close(ch)
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
				SafeClose(m.StopChan)
				break loop
			default:
				// Ignore unsupported control codes
			}
		case <-m.StopChan:
			changes <- svc.Status{State: svc.StopPending}
			break loop
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

// GetSharedTempDir returns the consistent shared temp directory path (C:\Windows\Temp by default).
func GetSharedTempDir() string {
	sharedTemp := "C:\\Windows\\Temp"
	if envRoot := os.Getenv("SystemRoot"); envRoot != "" {
		sharedTemp = filepath.Join(envRoot, "Temp")
	}
	return sharedTemp
}

// RunInUserSession spawns WinMon.exe as a helper inside the active console session.
func RunInUserSession(args string, timeout time.Duration) error {
	exePath, err := os.Executable()
	if err != nil {
		logDebug("os.Executable failed: %v", err)
		return err
	}
	logDebug("RunInUserSession started. exePath: %s, args: %s", exePath, args)

	sessionID := windows.WTSGetActiveConsoleSessionId()
	logDebug("Active console session ID: %d", sessionID)
	if sessionID == 0xFFFFFFFF {
		logDebug("No active console session found")
		return fmt.Errorf("no active console session found")
	}

	var userToken windows.Token
	err = windows.WTSQueryUserToken(sessionID, &userToken)
	if err != nil {
		logDebug("WTSQueryUserToken failed: %v", err)
		return fmt.Errorf("no user logged in on active console session (WTSQueryUserToken failed: %v)", err)
	}
	defer userToken.Close()

	var dupToken windows.Token
	err = windows.DuplicateTokenEx(
		userToken,
		windows.TOKEN_ALL_ACCESS,
		nil,
		windows.SecurityIdentification,
		windows.TokenPrimary,
		&dupToken,
	)
	if err != nil {
		logDebug("DuplicateTokenEx failed: %v", err)
		return fmt.Errorf("failed to duplicate user token: %v", err)
	}
	defer dupToken.Close()

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	desktopStr := "winsta0\\default"
	desktopUTF16, _ := windows.UTF16PtrFromString(desktopStr)
	si.Desktop = desktopUTF16

	var pi windows.ProcessInformation

	cmdLine := fmt.Sprintf("\"%s\" %s", exePath, args)
	cmdLineUTF16, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		logDebug("UTF16PtrFromString failed: %v", err)
		return err
	}

	logDebug("Spawning process: %s", cmdLine)
	err = windows.CreateProcessAsUser(
		dupToken,
		nil,
		cmdLineUTF16,
		nil,
		nil,
		false,
		windows.CREATE_NO_WINDOW,
		nil,
		nil,
		&si,
		&pi,
	)
	if err != nil {
		logDebug("CreateProcessAsUser failed: %v", err)
		return fmt.Errorf("CreateProcessAsUser failed: %v", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	logDebug("Process spawned successfully. PID: %d, ThreadID: %d", pi.ProcessId, pi.ThreadId)

	event, err := windows.WaitForSingleObject(pi.Process, uint32(timeout.Milliseconds()))
	if err != nil {
		logDebug("WaitForSingleObject failed: %v", err)
		return fmt.Errorf("failed waiting for helper process: %v", err)
	}
	if event == uint32(windows.WAIT_TIMEOUT) {
		windows.TerminateProcess(pi.Process, 1)
		logDebug("Helper process timed out")
		return fmt.Errorf("helper process timed out after %s", timeout)
	}

	var exitCode uint32
	err = windows.GetExitCodeProcess(pi.Process, &exitCode)
	if err != nil {
		logDebug("GetExitCodeProcess failed: %v", err)
		return fmt.Errorf("failed to get helper process exit code: %v", err)
	}
	logDebug("Helper process exited. ExitCode: %d", exitCode)
	if exitCode != 0 {
		return fmt.Errorf("helper process exited with non-zero code %d", exitCode)
	}

	return nil
}

// IsPipeListening checks if a named pipe is available for connection.
func IsPipeListening(pipeName string) bool {
	pipePathUTF16, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		return false
	}
	hPipe, err := windows.CreateFile(
		pipePathUTF16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err == nil {
		windows.CloseHandle(hPipe)
		return true
	}
	if err == windows.ERROR_PIPE_BUSY {
		return true
	}
	return false
}

// EnsureUserAgentRunning checks if the IPC pipe for the user agent is responsive, spawning winmon.exe -session-agent if needed.
func EnsureUserAgentRunning() error {
	if IsPipeListening(PipeName) {
		return nil
	}

	// Pipe not responding or unavailable; spawn persistent user agent in user session
	logDebug("User agent IPC pipe unavailable. Spawning persistent session agent...")
	return SpawnUserAgentInUserSession()
}

// SpawnUserAgentInUserSession spawns WinMon.exe as a persistent background daemon (-session-agent) inside the active user console session.
func SpawnUserAgentInUserSession() error {
	exePath, err := os.Executable()
	if err != nil {
		logDebug("os.Executable failed: %v", err)
		return err
	}

	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID == 0xFFFFFFFF {
		return fmt.Errorf("no active console session found")
	}

	var userToken windows.Token
	err = windows.WTSQueryUserToken(sessionID, &userToken)
	if err != nil {
		return fmt.Errorf("no user logged in on active console session (WTSQueryUserToken failed: %v)", err)
	}
	defer userToken.Close()

	var dupToken windows.Token
	err = windows.DuplicateTokenEx(
		userToken,
		windows.TOKEN_ALL_ACCESS,
		nil,
		windows.SecurityIdentification,
		windows.TokenPrimary,
		&dupToken,
	)
	if err != nil {
		return fmt.Errorf("failed to duplicate user token: %v", err)
	}
	defer dupToken.Close()

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	desktopStr := "winsta0\\default"
	desktopUTF16, _ := windows.UTF16PtrFromString(desktopStr)
	si.Desktop = desktopUTF16

	var pi windows.ProcessInformation

	cmdLine := fmt.Sprintf("\"%s\" -session-agent", exePath)
	cmdLineUTF16, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return err
	}

	logDebug("Spawning persistent session agent process: %s", cmdLine)
	err = windows.CreateProcessAsUser(
		dupToken,
		nil,
		cmdLineUTF16,
		nil,
		nil,
		false,
		windows.CREATE_NO_WINDOW,
		nil,
		nil,
		&si,
		&pi,
	)
	if err != nil {
		logDebug("CreateProcessAsUser failed: %v", err)
		return fmt.Errorf("CreateProcessAsUser failed: %v", err)
	}
	windows.CloseHandle(pi.Process)
	windows.CloseHandle(pi.Thread)
	logDebug("Persistent Session Agent spawned successfully. PID: %d", pi.ProcessId)

	// Wait up to 3 seconds for named pipe to be ready
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if IsPipeListening(PipeName) {
			return nil
		}
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

func killExistingTargetProcess() {
	_ = StopService("WinMon")
	time.Sleep(300 * time.Millisecond)

	currentPID := os.Getpid()
	killCmd := fmt.Sprintf("powershell -NoProfile -NonInteractive -Command \"Get-Process winmon -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne %d } | Stop-Process -Force\"", currentPID)
	c := exec.Command("cmd", "/c", killCmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = c.Run()
	time.Sleep(300 * time.Millisecond)
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

	// Define standard installation folder in Program Files
	targetDir := `C:\Program Files\WinMon`
	targetExePath := filepath.Join(targetDir, "winmon.exe")

	// If we are not already running from the target path, copy ourselves there
	if !strings.EqualFold(exePath, targetExePath) {
		// Stop any active service or background agent that might lock the destination file
		killExistingTargetProcess()

		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
		}

		if err := copyFile(exePath, targetExePath); err != nil {
			return fmt.Errorf("failed to copy executable to %s: %w", targetExePath, err)
		}

		// Copy config.json and state.json if present in the source folder
		srcDir := filepath.Dir(exePath)
		srcConfig := filepath.Join(srcDir, "config.json")
		if _, err := os.Stat(srcConfig); err == nil {
			_ = copyFile(srcConfig, filepath.Join(targetDir, "config.json"))
		} else if _, err := os.Stat("config.json"); err == nil {
			_ = copyFile("config.json", filepath.Join(targetDir, "config.json"))
		}

		srcState := filepath.Join(srcDir, "state.json")
		if _, err := os.Stat(srcState); err == nil {
			_ = copyFile(srcState, filepath.Join(targetDir, "state.json"))
		}

		exePath = targetExePath
	}

	quotedExePath := fmt.Sprintf("\"%s\"", exePath)
	s, err := m.CreateService(name, quotedExePath, mgr.Config{
		DisplayName: displayName,
		Description: desc,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		// If service already exists, open it and update BinaryPathName
		var openErr error
		s, openErr = m.OpenService(name)
		if openErr != nil {
			return fmt.Errorf("failed to create or open service %s: %w", name, err)
		}
		c, cfgErr := s.Config()
		if cfgErr == nil {
			c.BinaryPathName = quotedExePath
			c.DisplayName = displayName
			c.Description = desc
			c.StartType = mgr.StartAutomatic
			_ = s.UpdateConfig(c)
		}
	}
	defer s.Close()

	// Explicitly set ImagePath and Description in registry to guarantee proper quoting
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `System\CurrentControlSet\Services\`+name, registry.WRITE)
	if err == nil {
		_ = k.SetStringValue("ImagePath", quotedExePath)
		_ = k.SetStringValue("Description", desc)
		k.Close()
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
