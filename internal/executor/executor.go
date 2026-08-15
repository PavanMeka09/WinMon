package executor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"winmon/internal/audio"
	"winmon/internal/clipboard"
	"winmon/internal/device"
	"winmon/internal/display"
	"winmon/internal/input"
	"winmon/internal/media"
	"winmon/internal/notifications"
	"winmon/internal/service"
	"winmon/internal/shell"
)

// ResultKind identifies the payload format produced by a Host Action.
type ResultKind int

const (
	ResultKindText ResultKind = iota
	ResultKindPhoto
	ResultKindAnimation
	ResultKindAudio
	ResultKindFile
)

// Result represents the typed outcome of a Host Action execution.
type Result struct {
	Kind     ResultKind
	Text     string
	FilePath string
	Caption  string
	Duration time.Duration
	Cleanup  func()
}

// ActionType defines the category of host action to perform.
type ActionType string

const (
	ActionScreenshot   ActionType = "screenshot"
	ActionWebcam       ActionType = "webcam"
	ActionScreenRecord ActionType = "screenrecord"
	ActionListen       ActionType = "listen"
	ActionClipboard    ActionType = "clipboard"
	ActionSetClipboard ActionType = "setclipboard"
	ActionHotkey       ActionType = "hotkey"
	ActionBrightness   ActionType = "brightness"
	ActionVolume       ActionType = "volume"
	ActionWallpaper    ActionType = "wallpaper"
	ActionSetWallpaper ActionType = "setwallpaper"
	ActionNotify       ActionType = "notify"
	ActionTTS          ActionType = "tts"
	ActionLock         ActionType = "lock"
	ActionCmd          ActionType = "cmd"
	ActionProcesses    ActionType = "processes"
	ActionKill         ActionType = "kill"
	ActionSysInfo      ActionType = "sysinfo"
)

type actionMeta struct {
	interactive bool
	filePrefix  string
	fileExt     string
	defaultKind ResultKind
	caption     string
}

var actionMetadata = map[ActionType]actionMeta{
	ActionScreenshot:   {interactive: true, filePrefix: "screenshot_", fileExt: ".jpg", defaultKind: ResultKindPhoto, caption: "📸 **Desktop Screenshot**"},
	ActionWebcam:       {interactive: true, filePrefix: "webcam_", fileExt: ".jpg", defaultKind: ResultKindPhoto, caption: "📹 **Webcam Photo**"},
	ActionScreenRecord: {interactive: true, filePrefix: "record_", fileExt: ".gif", defaultKind: ResultKindAnimation, caption: "🎥 **Screen Recording GIF**"},
	ActionListen:       {interactive: true, filePrefix: "audio_", fileExt: ".wav", defaultKind: ResultKindAudio, caption: "🎙️ **Microphone Audio Recording**"},
	ActionWallpaper:    {interactive: true, filePrefix: "wallpaper_", fileExt: "", defaultKind: ResultKindPhoto, caption: "🖼 **Desktop Wallpaper**"},
	ActionClipboard:    {interactive: true, filePrefix: "clipboard_", fileExt: ".txt", defaultKind: ResultKindText, caption: "📋 **Clipboard Content**"},
	ActionSetClipboard: {interactive: true, defaultKind: ResultKindText},
	ActionHotkey:       {interactive: true, defaultKind: ResultKindText},
	ActionBrightness:   {interactive: true, defaultKind: ResultKindText},
	ActionVolume:       {interactive: true, defaultKind: ResultKindText},
	ActionSetWallpaper: {interactive: true, defaultKind: ResultKindText},
	ActionNotify:       {interactive: true, defaultKind: ResultKindText},
	ActionTTS:          {interactive: true, defaultKind: ResultKindText},
	ActionLock:         {interactive: true, defaultKind: ResultKindText},
	ActionCmd:          {interactive: false, defaultKind: ResultKindText},
	ActionProcesses:    {interactive: false, defaultKind: ResultKindText},
	ActionKill:         {interactive: false, defaultKind: ResultKindText},
	ActionSysInfo:      {interactive: false, defaultKind: ResultKindText},
}

// IsInteractive returns true if the action requires the user's interactive desktop session.
func (t ActionType) IsInteractive() bool {
	if meta, ok := actionMetadata[t]; ok {
		return meta.interactive
	}
	return false
}

// TempFilePath generates a standardized temporary file path for the action type if it produces file output.
func (t ActionType) TempFilePath(ts int64) string {
	meta, ok := actionMetadata[t]
	if !ok || meta.filePrefix == "" {
		return ""
	}
	return filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("%s%d%s", meta.filePrefix, ts, meta.fileExt))
}

// Helper factories to deduplicate Result struct creation and temp file cleanup
func newMediaResult(kind ResultKind, filePath, caption string, dur time.Duration) *Result {
	formattedCaption := caption
	if dur > 0 {
		formattedCaption = fmt.Sprintf("%s (%d ms)", caption, dur.Milliseconds())
	}
	return &Result{
		Kind:     kind,
		FilePath: filePath,
		Caption:  formattedCaption,
		Duration: dur,
		Cleanup:  func() { _ = os.Remove(filePath) },
	}
}

func newTextResult(text string, dur time.Duration) *Result {
	return &Result{
		Kind:     ResultKindText,
		Text:     text,
		Duration: dur,
	}
}

// Action encapsulates the parameters for an action request.
type Action struct {
	Type       ActionType
	Args       []string
	FlatArgs   string
	OutputFile string
	Timeout    time.Duration
}

// ActionExecutor is the deep interface for executing operations against the host OS.
type ActionExecutor interface {
	Execute(ctx context.Context, action Action) (*Result, error)
}

// InProcessExecutor executes host actions directly within the calling process.
type InProcessExecutor struct{}

// NewInProcessExecutor creates an executor that runs actions in-process.
func NewInProcessExecutor() *InProcessExecutor {
	return &InProcessExecutor{}
}

// Execute performs the requested action directly using host APIs.
func (e *InProcessExecutor) Execute(ctx context.Context, action Action) (*Result, error) {
	start := time.Now()

	switch action.Type {
	case ActionScreenshot:
		tempPath := action.OutputFile
		if tempPath == "" {
			tempPath = action.Type.TempFilePath(time.Now().UnixNano())
		}
		if err := media.CaptureScreen(tempPath); err != nil {
			return nil, fmt.Errorf("failed to capture screenshot: %w", err)
		}
		return newMediaResult(ResultKindPhoto, tempPath, actionMetadata[ActionScreenshot].caption, time.Since(start)), nil

	case ActionWebcam:
		tempPath := action.OutputFile
		if tempPath == "" {
			tempPath = action.Type.TempFilePath(time.Now().UnixNano())
		}
		if err := media.CaptureWebcam(tempPath); err != nil {
			return nil, fmt.Errorf("failed to capture webcam: %w", err)
		}
		return newMediaResult(ResultKindPhoto, tempPath, actionMetadata[ActionWebcam].caption, time.Since(start)), nil

	case ActionScreenRecord:
		dur := parseDuration(action.FlatArgs)
		tempPath := action.OutputFile
		if tempPath == "" {
			tempPath = action.Type.TempFilePath(time.Now().UnixNano())
		}
		if err := media.RecordScreen(dur, tempPath); err != nil {
			return nil, fmt.Errorf("failed to record screen: %w", err)
		}
		return newMediaResult(ResultKindAnimation, tempPath, actionMetadata[ActionScreenRecord].caption, time.Since(start)), nil

	case ActionListen:
		dur := parseDuration(action.FlatArgs)
		tempPath := action.OutputFile
		if tempPath == "" {
			tempPath = action.Type.TempFilePath(time.Now().UnixNano())
		}
		if err := media.RecordAudio(dur, tempPath); err != nil {
			return nil, fmt.Errorf("failed to record audio: %w", err)
		}
		return newMediaResult(ResultKindAudio, tempPath, actionMetadata[ActionListen].caption, time.Since(start)), nil

	case ActionClipboard:
		if strings.TrimSpace(action.FlatArgs) != "" {
			if err := clipboard.SetClipboardLocal(action.FlatArgs); err != nil {
				return nil, fmt.Errorf("failed to set clipboard: %w", err)
			}
			return newTextResult("📋 Clipboard text updated successfully.", time.Since(start)), nil
		}
		txt, err := clipboard.GetClipboardLocal()
		if err != nil || strings.TrimSpace(txt) == "" {
			txt = "(Clipboard is empty or contains non-text data)"
		}
		return newTextResult(fmt.Sprintf("📋 **Clipboard Content:**\n```\n%s\n```", txt), time.Since(start)), nil

	case ActionSetClipboard:
		if err := clipboard.SetClipboardLocal(action.FlatArgs); err != nil {
			return nil, fmt.Errorf("failed to set clipboard: %w", err)
		}
		return newTextResult("📋 Clipboard text updated successfully.", time.Since(start)), nil

	case ActionHotkey:
		hotkey := strings.TrimSpace(action.FlatArgs)
		if hotkey == "" {
			return nil, fmt.Errorf("usage: /hotkey <keys> (e.g. /hotkey win+d or /hotkey ctrl+c)")
		}
		if err := input.TriggerHotkey(hotkey); err != nil {
			return nil, fmt.Errorf("failed to trigger hotkey: %w", err)
		}
		return newTextResult(fmt.Sprintf("⌨️ Hotkey `%s` executed successfully (%d ms).", hotkey, time.Since(start).Milliseconds()), time.Since(start)), nil

	case ActionBrightness:
		if len(action.Args) < 1 {
			return nil, fmt.Errorf("usage: /brightness <0-100>")
		}
		bri, err := strconv.Atoi(action.Args[0])
		if err != nil {
			return nil, fmt.Errorf("brightness must be an integer (0-100): %w", err)
		}
		if err := display.SetBrightness(bri); err != nil {
			return nil, fmt.Errorf("failed to set brightness: %w", err)
		}
		return newTextResult(fmt.Sprintf("🔆 Brightness set to **%d%%**.", bri), time.Since(start)), nil

	case ActionVolume:
		arg := strings.ToLower(strings.TrimSpace(action.FlatArgs))
		if arg == "" {
			return nil, fmt.Errorf("usage: /volume <0-100|mute|unmute>")
		}
		if arg == "mute" {
			if err := audio.SetMute(true); err != nil {
				return nil, fmt.Errorf("failed to mute volume: %w", err)
			}
			return newTextResult("🔇 Master audio muted.", time.Since(start)), nil
		} else if arg == "unmute" {
			if err := audio.SetMute(false); err != nil {
				return nil, fmt.Errorf("failed to unmute volume: %w", err)
			}
			return newTextResult("🔊 Master audio unmuted.", time.Since(start)), nil
		} else {
			vol, err := strconv.Atoi(arg)
			if err != nil {
				return nil, fmt.Errorf("volume must be an integer (0-100) or 'mute'/'unmute'")
			}
			if err := audio.SetVolume(vol); err != nil {
				return nil, fmt.Errorf("failed to set volume: %w", err)
			}
			return newTextResult(fmt.Sprintf("🔊 Master volume set to **%d%%**.", vol), time.Since(start)), nil
		}

	case ActionWallpaper:
		wallPath, err := display.GetWallpaperPath()
		if err != nil {
			return nil, fmt.Errorf("failed to get wallpaper path: %w", err)
		}
		data, err := os.ReadFile(wallPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read wallpaper file at %s: %w", wallPath, err)
		}
		ext := strings.ToLower(filepath.Ext(wallPath))
		if ext == "" {
			ext = imageExtFromMagic(data)
		}
		tempPath := filepath.Join(service.GetSharedTempDir(), fmt.Sprintf("wallpaper_%d%s", time.Now().UnixNano(), ext))
		if err := os.WriteFile(tempPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to save temp wallpaper: %w", err)
		}
		dur := time.Since(start)
		isPhoto := ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp"
		kind := ResultKindFile
		if isPhoto {
			kind = ResultKindPhoto
		}
		return newMediaResult(kind, tempPath, actionMetadata[ActionWallpaper].caption, dur), nil

	case ActionSetWallpaper:
		path := strings.TrimSpace(action.FlatArgs)
		if path == "" {
			return nil, fmt.Errorf("missing wallpaper image path")
		}
		if err := display.SetWallpaperLocal(path); err != nil {
			return nil, fmt.Errorf("failed to set wallpaper: %w", err)
		}
		return newTextResult("🖼 Desktop wallpaper updated successfully.", time.Since(start)), nil

	case ActionNotify:
		parts := strings.Split(action.FlatArgs, "|")
		title := "WinMon Notification"
		msg := action.FlatArgs
		if len(parts) > 1 {
			title = strings.TrimSpace(parts[0])
			msg = strings.TrimSpace(parts[1])
		}
		err := notifications.ShowToastLocal(title, msg)
		if err != nil {
			if errAlert := notifications.ShowAlert(title, msg); errAlert != nil {
				return nil, fmt.Errorf("failed to show notification: %w", err)
			}
		}
		return newTextResult("🔔 Notification displayed on PC screen.", time.Since(start)), nil

	case ActionTTS:
		if strings.TrimSpace(action.FlatArgs) == "" {
			return nil, fmt.Errorf("usage: /tts <text>")
		}
		if err := audio.SpeakTTS(action.FlatArgs); err != nil {
			return nil, fmt.Errorf("failed to speak text: %w", err)
		}
		return newTextResult("🗣️ Text spoken via PC speakers.", time.Since(start)), nil

	case ActionLock:
		if err := display.LockWorkstation(); err != nil {
			return nil, fmt.Errorf("failed to lock workstation: %w", err)
		}
		return newTextResult("🔒 Windows workstation locked.", time.Since(start)), nil

	case ActionCmd:
		if len(action.Args) < 1 {
			return nil, fmt.Errorf("usage: /cmd <command>")
		}
		timeout := action.Timeout
		if timeout <= 0 {
			timeout = 25 * time.Second
		}
		execStr := strings.Join(action.Args, " ")
		out, err := shell.ExecuteCommand(execStr, timeout)
		dur := time.Since(start)
		if len(out) > 3800 {
			out = out[:3800] + "\n... [truncated]"
		}
		if err != nil {
			return newTextResult(fmt.Sprintf("[Error] **Command Execution Error:**\n```\n%s\nError: %v\n```", out, err), dur), nil
		}
		return newTextResult(fmt.Sprintf("**Command Output:** (%d ms)\n```\n%s\n```", dur.Milliseconds(), out), dur), nil

	case ActionProcesses:
		psCmd := `Get-Process | Sort-Object WorkingSet64 -Descending | Select-Object -First 25 | Format-Table Id, ProcessName, @{Name='RAM (MB)';Expression={[math]::Round($_.WorkingSet64/1MB, 1)}} -AutoSize | Out-String`
		out, err := shell.ExecuteCommand("powershell -NoProfile -NonInteractive -Command \""+psCmd+"\"", 15*time.Second)
		dur := time.Since(start)
		if err != nil || strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("failed to list processes: %v", err)
		}
		return newTextResult(fmt.Sprintf("⚙️ **Top Processes by Memory Usage:** (%d ms)\n```\n%s\n```", dur.Milliseconds(), strings.TrimSpace(out)), dur), nil

	case ActionKill:
		if len(action.Args) < 1 {
			return nil, fmt.Errorf("usage: /kill <PID|ProcessName> (e.g. `/kill 1234` or `/kill notepad.exe`)")
		}
		target := strings.TrimSpace(action.Args[0])
		var killCmd string
		if _, err := strconv.Atoi(target); err == nil {
			killCmd = fmt.Sprintf("taskkill /F /PID %s", target)
		} else {
			if !strings.HasSuffix(strings.ToLower(target), ".exe") {
				target += ".exe"
			}
			killCmd = fmt.Sprintf("taskkill /F /IM \"%s\"", target)
		}
		out, err := shell.ExecuteCommand(killCmd, 10*time.Second)
		dur := time.Since(start)
		if err != nil {
			return nil, fmt.Errorf("failed to terminate process `%s`:\n```\n%s\n```", target, out)
		}
		return newTextResult(fmt.Sprintf("💀 Process `%s` terminated successfully:\n```\n%s\n```", target, strings.TrimSpace(out)), dur), nil

	case ActionSysInfo:
		info, err := device.GetDeviceInfo(device.GetComputerName(), device.GetComputerUUID(), "1.0.0")
		if err != nil {
			return nil, fmt.Errorf("failed to get system info: %w", err)
		}
		status, _ := device.GetStatus()
		msg := fmt.Sprintf("📊 **System Information & Hardware Metrics**\n\n"+
			"• **Device Name:** `%s` (ID: `%s`)\n"+
			"• **Computer Name:** `%s`\n"+
			"• **OS:** `%s`\n"+
			"• **IP Address:** `%s`\n"+
			"• **Uptime:** `%s`\n"+
			"• **WinMon Version:** `%s`\n",
			info.DeviceName, info.DeviceID, info.PCName, info.OS, info.IPAddress, device.FormatDuration(info.Uptime), info.Version)

		if status != nil {
			ramUsedGB := status.RAMTotalGB - status.RAMFreeGB
			diskUsedGB := status.DiskTotalGB - status.DiskFreeGB
			msg += fmt.Sprintf("\n⚡ **Hardware Metrics:**\n"+
				"• **CPU Load:** `%.1f%%`\n"+
				"• **RAM Usage:** `%.1f%%` (%.1f GB / %.1f GB)\n"+
				"• **Disk Usage:** `%.1f%%` (%.1f GB / %.1f GB)\n",
				status.CPUPercent, status.RAMPercent, ramUsedGB, status.RAMTotalGB,
				status.DiskPercent, diskUsedGB, status.DiskTotalGB)
			if status.BatteryCharge >= 0 {
				msg += fmt.Sprintf("• **Battery:** `%d%%` (%s)\n", status.BatteryCharge, status.BatteryStatus)
			}
		}
		return newTextResult(msg, time.Since(start)), nil

	default:
		return nil, fmt.Errorf("unsupported action type: %s", action.Type)
	}
}

// IPCExecutor executes host actions by forwarding them over Named Pipe IPC to Session 1.
type IPCExecutor struct {
	timeout time.Duration
}

// NewIPCExecutor creates an IPC executor with the specified timeout.
func NewIPCExecutor(timeout time.Duration) *IPCExecutor {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &IPCExecutor{timeout: timeout}
}

// Execute marshals the action across the named pipe and translates the IPC response.
func (e *IPCExecutor) Execute(ctx context.Context, action Action) (*Result, error) {
	start := time.Now()
	tempPath := action.OutputFile
	if tempPath == "" {
		tempPath = action.Type.TempFilePath(time.Now().UnixNano())
	}

	cmdStr := "/" + string(action.Type)
	resp, err := service.SendIPCCommand(service.IPCRequest{
		Cmd:        cmdStr,
		Args:       action.Args,
		FlatArgs:   action.FlatArgs,
		OutputFile: tempPath,
	}, e.timeout)
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, fmt.Errorf("session agent IPC error: %w", err)
	}

	if !resp.Success {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, fmt.Errorf("IPC command error: %s", resp.Error)
	}

	outPath := tempPath
	if resp.OutputFile != "" {
		outPath = resp.OutputFile
	}

	dur := time.Since(start)
	meta := actionMetadata[action.Type]

	// 1. OutputText returned from Session 1 persistent agent takes precedence for text actions
	if resp.OutputText != "" {
		return newTextResult(resp.OutputText, dur), nil
	}

	// 2. Media actions
	if meta.defaultKind == ResultKindPhoto || meta.defaultKind == ResultKindAnimation || meta.defaultKind == ResultKindAudio {
		if action.Type == ActionWallpaper {
			wallFile := findWallpaperFile(outPath)
			if wallFile == "" {
				return nil, fmt.Errorf("failed to retrieve wallpaper from session agent")
			}
			ext := strings.ToLower(filepath.Ext(wallFile))
			isPhoto := ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp"
			kind := ResultKindFile
			if isPhoto {
				kind = ResultKindPhoto
			}
			return newMediaResult(kind, wallFile, meta.caption, dur), nil
		}
		return newMediaResult(meta.defaultKind, outPath, meta.caption, dur), nil
	}

	// 3. File-backed text actions (e.g. clipboard read from file fallback)
	if outPath != "" && action.Type == ActionClipboard {
		if data, err := os.ReadFile(outPath); err == nil {
			_ = os.Remove(outPath)
			txt := string(data)
			if strings.TrimSpace(action.FlatArgs) != "" {
				return newTextResult(txt, dur), nil
			}
			return newTextResult(fmt.Sprintf("📋 **Clipboard Content:**\n```\n%s\n```", txt), dur), nil
		}
	}

	// 4. Default confirmation messages
	switch action.Type {
	case ActionNotify:
		return newTextResult("🔔 Notification displayed on PC screen.", dur), nil
	case ActionHotkey:
		return newTextResult(fmt.Sprintf("⌨️ Hotkey trigger executed (%d ms).", dur.Milliseconds()), dur), nil
	case ActionBrightness:
		return newTextResult(fmt.Sprintf("🔆 Brightness set to **%s%%**.", action.FlatArgs), dur), nil
	case ActionVolume:
		return newTextResult(fmt.Sprintf("🔊 Volume operation `%s` executed successfully.", action.FlatArgs), dur), nil
	case ActionLock:
		return newTextResult("🔒 Windows workstation locked.", dur), nil
	case ActionTTS:
		return newTextResult("🗣️ Text spoken via PC speakers.", dur), nil
	case ActionSetWallpaper:
		return newTextResult("🖼 Desktop wallpaper updated successfully.", dur), nil
	default:
		return newTextResult(fmt.Sprintf("✅ Action `%s` executed successfully (%d ms).", action.Type, dur.Milliseconds()), dur), nil
	}
}

// AutoExecutor dynamically selects between InProcessExecutor and IPCExecutor based on whether the process runs as a Windows Service.
type AutoExecutor struct {
	inProcess *InProcessExecutor
	ipc       *IPCExecutor
}

// NewAutoExecutor constructs a context-aware executor.
func NewAutoExecutor() *AutoExecutor {
	return &AutoExecutor{
		inProcess: NewInProcessExecutor(),
		ipc:       NewIPCExecutor(60 * time.Second),
	}
}

// Execute routes the action to the appropriate adapter based on action interactivity and runtime service mode.
func (a *AutoExecutor) Execute(ctx context.Context, action Action) (*Result, error) {
	if service.IsRunningAsService() && action.Type.IsInteractive() {
		return a.ipc.Execute(ctx, action)
	}
	return a.inProcess.Execute(ctx, action)
}

// MockExecutor is a thread-safe in-memory adapter satisfying ActionExecutor for deterministic tests.
type MockExecutor struct {
	mu         sync.Mutex
	LastAction Action
	ReturnRes  *Result
	ReturnErr  error
	CallCount  int
	Handler    func(ctx context.Context, action Action) (*Result, error)
}

// NewMockExecutor returns an initialized MockExecutor.
func NewMockExecutor(res *Result, err error) *MockExecutor {
	return &MockExecutor{
		ReturnRes: res,
		ReturnErr: err,
	}
}

// Execute records the action and returns configured results or executes the custom handler.
func (m *MockExecutor) Execute(ctx context.Context, action Action) (*Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CallCount++
	m.LastAction = action
	if m.Handler != nil {
		return m.Handler(ctx, action)
	}
	if m.ReturnErr != nil {
		return nil, m.ReturnErr
	}
	return m.ReturnRes, nil
}

// RunSessionHelper executes a single command inside the user's interactive desktop session.
func RunSessionHelper(cmd string, args string, outputFile string) (string, error) {
	actionType := ActionType(strings.TrimPrefix(cmd, "/"))
	res, err := NewInProcessExecutor().Execute(context.Background(), Action{
		Type:       actionType,
		Args:       strings.Fields(args),
		FlatArgs:   args,
		OutputFile: outputFile,
	})
	if err != nil {
		return "", err
	}
	if res.FilePath != "" {
		return res.FilePath, nil
	}
	return "", nil
}

// RunSessionAgentLoop runs the persistent Named Pipe IPC listener in Session 1.
func RunSessionAgentLoop() error {
	log.Println("Starting WinMon Persistent Session Agent (Session 1 IPC Listener)...")
	inProc := NewInProcessExecutor()
	return service.StartIPCAgentServer(func(req service.IPCRequest) service.IPCResponse {
		actionType := ActionType(strings.TrimPrefix(req.Cmd, "/"))
		res, err := inProc.Execute(context.Background(), Action{
			Type:       actionType,
			Args:       req.Args,
			FlatArgs:   req.FlatArgs,
			OutputFile: req.OutputFile,
		})
		if err != nil {
			return service.IPCResponse{
				Success: false,
				Error:   err.Error(),
			}
		}
		return service.IPCResponse{
			Success:    true,
			OutputFile: res.FilePath,
			OutputText: res.Text,
		}
	})
}

func parseDuration(arg string) time.Duration {
	d, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || d <= 0 {
		return 5 * time.Second
	}
	return time.Duration(d) * time.Second
}

func findWallpaperFile(basePath string) string {
	if basePath == "" {
		return ""
	}
	if info, err := os.Stat(basePath); err == nil && !info.IsDir() {
		return basePath
	}
	matches, err := filepath.Glob(basePath + ".*")
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func imageExtFromMagic(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ".jpg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return ".gif"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	case len(data) >= 2 && data[0] == 0x42 && data[1] == 0x4D:
		return ".bmp"
	default:
		return ".bin"
	}
}
