package service

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const PipeName = `\\.\pipe\WinMonIPC`

type IPCRequest struct {
	Cmd      string   `json:"cmd"`
	Args     []string `json:"args"`
	FlatArgs string   `json:"flat_args"`
}

type IPCResponse struct {
	Success    bool   `json:"success"`
	Error      string `json:"error"`
	OutputFile string `json:"output_file,omitempty"`
	OutputText string `json:"output_text,omitempty"`
}

var ipcMu sync.Mutex

// SendIPCCommand is called by the Service (Session 0) to send a command request to the Persistent User Agent (Session 1) over Named Pipe IPC.
func SendIPCCommand(req IPCRequest, timeout time.Duration) (*IPCResponse, error) {
	ipcMu.Lock()
	defer ipcMu.Unlock()

	// Ensure the User Agent is active in Session 1
	if err := EnsureUserAgentRunning(); err != nil {
		return nil, fmt.Errorf("failed to ensure user agent is running: %w", err)
	}

	pipePathUTF16, err := windows.UTF16PtrFromString(PipeName)
	if err != nil {
		return nil, err
	}

	var hPipe windows.Handle
	deadline := time.Now().Add(timeout)
	for {
		hPipe, err = windows.CreateFile(
			pipePathUTF16,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout connecting to IPC pipe %s: %w", PipeName, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	pipeFile := os.NewFile(uintptr(hPipe), PipeName)
	defer pipeFile.Close()

	// Send Request JSON
	if err := json.NewEncoder(pipeFile).Encode(req); err != nil {
		return nil, fmt.Errorf("IPC write request failed: %w", err)
	}

	// Read Response JSON
	var resp IPCResponse
	if err := json.NewDecoder(pipeFile).Decode(&resp); err != nil {
		return nil, fmt.Errorf("IPC read response failed: %w", err)
	}

	return &resp, nil
}

// StartIPCAgentServer runs in the Persistent User Agent process (Session 1), serving incoming IPC requests from Session 0.
func StartIPCAgentServer(handler func(req IPCRequest) IPCResponse) error {
	pipeNameUTF16, err := windows.UTF16PtrFromString(PipeName)
	if err != nil {
		return err
	}

	for {
		hPipe, err := windows.CreateNamedPipe(
			pipeNameUTF16,
			windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES,
			65536,
			65536,
			0,
			nil,
		)
		if err != nil {
			logDebug("CreateNamedPipe failed: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		err = windows.ConnectNamedPipe(hPipe, nil)
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			windows.CloseHandle(hPipe)
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// Handle request in a closure to ensure cleanup per connection
		func(h windows.Handle) {
			pipeFile := os.NewFile(uintptr(h), PipeName)
			defer func() {
				windows.DisconnectNamedPipe(h)
				pipeFile.Close()
			}()

			var req IPCRequest
			if err := json.NewDecoder(pipeFile).Decode(&req); err != nil {
				logDebug("IPC server decode error: %v", err)
				return
			}

			resp := handler(req)
			_ = json.NewEncoder(pipeFile).Encode(resp)
		}(hPipe)
	}
}
