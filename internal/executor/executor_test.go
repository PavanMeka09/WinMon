package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10", 10 * time.Second},
		{"5", 5 * time.Second},
		{"0", 5 * time.Second},
		{"-2", 5 * time.Second},
		{"abc", 5 * time.Second},
		{"", 5 * time.Second},
	}

	for _, tt := range tests {
		got := parseDuration(tt.input)
		if got != tt.expected {
			t.Errorf("parseDuration(%q) = %v; expected %v", tt.input, got, tt.expected)
		}
	}
}

func TestImageExtFromMagic(t *testing.T) {
	tests := []struct {
		data     []byte
		expected string
	}{
		{[]byte{0xFF, 0xD8, 0xFF, 0x00}, ".jpg"},
		{[]byte("\x89PNG\r\n\x1a\n\x00\x00"), ".png"},
		{[]byte("GIF89a\x00\x00"), ".gif"},
		{[]byte("GIF87a\x00\x00"), ".gif"},
		{[]byte("RIFF\x00\x00\x00\x00WEBP"), ".webp"},
		{[]byte("BM\x00\x00"), ".bmp"},
		{[]byte("UNKNOWN_DATA"), ".bin"},
	}

	for _, tt := range tests {
		got := imageExtFromMagic(tt.data)
		if got != tt.expected {
			t.Errorf("imageExtFromMagic(%q) = %q; expected %q", tt.data, got, tt.expected)
		}
	}
}

func TestInProcessExecutor_CmdExecution(t *testing.T) {
	exec := NewInProcessExecutor()
	res, err := exec.Execute(context.Background(), Action{
		Type:    ActionCmd,
		Args:    []string{"echo", "hello_winmon"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error executing echo command: %v", err)
	}

	if res.Kind != ResultKindText {
		t.Errorf("expected ResultKindText, got %v", res.Kind)
	}
	if !strings.Contains(res.Text, "hello_winmon") {
		t.Errorf("expected output to contain 'hello_winmon', got %q", res.Text)
	}
}

func TestInProcessExecutor_ClipboardText(t *testing.T) {
	exec := NewInProcessExecutor()

	// Set clipboard
	setRes, err := exec.Execute(context.Background(), Action{
		Type:     ActionClipboard,
		FlatArgs: "test_clipboard_data_123",
	})
	if err != nil {
		t.Skipf("skipping clipboard test (headless/CI environment without GUI session): %v", err)
	}
	if setRes.Kind != ResultKindText {
		t.Errorf("expected ResultKindText, got %v", setRes.Kind)
	}

	// Read clipboard
	getRes, err := exec.Execute(context.Background(), Action{
		Type: ActionClipboard,
	})
	if err != nil {
		t.Fatalf("unexpected error reading clipboard: %v", err)
	}
	if !strings.Contains(getRes.Text, "test_clipboard_data_123") {
		t.Errorf("expected clipboard content to contain 'test_clipboard_data_123', got %q", getRes.Text)
	}
}

func TestInProcessExecutor_InvalidAction(t *testing.T) {
	exec := NewInProcessExecutor()
	_, err := exec.Execute(context.Background(), Action{
		Type: ActionType("non_existent_action"),
	})
	if err == nil {
		t.Error("expected error for non-existent action type, got nil")
	}
}

func TestFindWallpaperFile(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "wallpaper_test")

	// 1. Exact match
	if err := os.WriteFile(basePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := findWallpaperFile(basePath); got != basePath {
		t.Errorf("findWallpaperFile exact = %q; expected %q", got, basePath)
	}
	_ = os.Remove(basePath)

	// 2. Extension match
	jpgPath := basePath + ".jpg"
	if err := os.WriteFile(jpgPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := findWallpaperFile(basePath); got != jpgPath {
		t.Errorf("findWallpaperFile with ext = %q; expected %q", got, jpgPath)
	}
}

func TestActionType_IsInteractive(t *testing.T) {
	interactiveActions := []ActionType{
		ActionScreenshot, ActionWebcam, ActionScreenRecord, ActionListen,
		ActionClipboard, ActionSetClipboard, ActionHotkey, ActionBrightness,
		ActionVolume, ActionWallpaper, ActionSetWallpaper, ActionNotify,
		ActionTTS, ActionLock,
	}

	for _, a := range interactiveActions {
		if !a.IsInteractive() {
			t.Errorf("expected action %q to be interactive", a)
		}
	}

	nonInteractiveActions := []ActionType{
		ActionCmd, ActionProcesses, ActionKill, ActionSysInfo,
	}

	for _, a := range nonInteractiveActions {
		if a.IsInteractive() {
			t.Errorf("expected action %q to NOT be interactive", a)
		}
	}
}

func TestActionType_TempFilePath(t *testing.T) {
	p := ActionScreenshot.TempFilePath(12345)
	if !strings.Contains(p, "screenshot_12345.jpg") {
		t.Errorf("unexpected temp file path for screenshot: %s", p)
	}

	empty := ActionCmd.TempFilePath(12345)
	if empty != "" {
		t.Errorf("expected empty temp file path for non-file action, got: %s", empty)
	}
}

func TestMockExecutor_Execution(t *testing.T) {
	expectedRes := &Result{Kind: ResultKindText, Text: "mocked"}
	mock := NewMockExecutor(expectedRes, nil)

	res, err := mock.Execute(context.Background(), Action{Type: ActionScreenshot})
	if err != nil || res.Text != "mocked" || mock.CallCount != 1 {
		t.Errorf("unexpected mock result: res=%v, err=%v, count=%d", res, err, mock.CallCount)
	}

	// Test error return
	mockErr := NewMockExecutor(nil, errors.New("err"))
	_, err = mockErr.Execute(context.Background(), Action{Type: ActionScreenshot})
	if err == nil {
		t.Error("expected error from mock executor, got nil")
	}

	// Test custom handler
	customMock := &MockExecutor{
		Handler: func(ctx context.Context, action Action) (*Result, error) {
			return &Result{Kind: ResultKindText, Text: "custom_" + string(action.Type)}, nil
		},
	}
	res, err = customMock.Execute(context.Background(), Action{Type: ActionVolume})
	if err != nil || res.Text != "custom_volume" {
		t.Errorf("unexpected custom handler output: %v", res)
	}
}

func TestNewMediaResult_Cleanup(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "dummy_media.jpg")
	if err := os.WriteFile(tempFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	res := newMediaResult(ResultKindPhoto, tempFile, "📸 Test Photo", 100*time.Millisecond)
	if res.Kind != ResultKindPhoto || !strings.Contains(res.Caption, "(100 ms)") {
		t.Errorf("unexpected media result: %+v", res)
	}

	if res.Cleanup != nil {
		res.Cleanup()
	}
	if _, err := os.Stat(tempFile); !os.IsNotExist(err) {
		t.Error("expected temp file to be deleted by cleanup handler")
	}
}
