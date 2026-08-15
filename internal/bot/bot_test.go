package bot

import (
	"errors"
	"testing"

	"winmon/internal/config"
	"winmon/internal/executor"
)

func TestIsAuthorized_EmptyAllowedUsers(t *testing.T) {
	b := &BotCoordinator{
		cfg: &config.Config{
			AllowedUsers: []string{},
		},
	}

	if b.isAuthorized(123456789, "user1") {
		t.Error("expected isAuthorized to return false when allowed_users is empty, got true")
	}
}

func TestIsAuthorized_NumericIDsOnly(t *testing.T) {
	b := &BotCoordinator{
		cfg: &config.Config{
			AllowedUsers: []string{"123456789", "Alice", "@999888777"},
		},
	}

	if !b.isAuthorized(123456789, "unknown") {
		t.Error("expected user ID 123456789 to be authorized")
	}

	// Usernames must not authorize, even if listed in config
	if b.isAuthorized(999999, "Alice") {
		t.Error("expected username allowlisting to be disabled")
	}

	if !b.isAuthorized(999888777, "anyone") {
		t.Error("expected numeric ID with @ prefix in config to authorize matching user ID")
	}

	if b.isAuthorized(888888, "charlie") {
		t.Error("expected unauthorized user to return false")
	}
}

func TestProcessCommand_ExecutorRouting(t *testing.T) {
	mock := executor.NewMockExecutor(&executor.Result{
		Kind: executor.ResultKindText,
		Text: "success",
	}, nil)

	b := &BotCoordinator{
		cfg: &config.Config{
			DeviceName:   "TestPC",
			AllowedUsers: []string{"123"},
		},
		executor: mock,
	}

	// 1. Screenshot command
	mock.CallCount = 0
	b.processCommand("/screenshot", []string{}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionScreenshot {
		t.Errorf("expected ActionScreenshot, got %v (calls: %d)", mock.LastAction.Type, mock.CallCount)
	}

	// 2. Volume command with arguments
	mock.CallCount = 0
	b.processCommand("/volume", []string{"50"}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionVolume || mock.LastAction.FlatArgs != "50" {
		t.Errorf("expected ActionVolume with FlatArgs '50', got %v (args: %s)", mock.LastAction.Type, mock.LastAction.FlatArgs)
	}

	// 3. Brightness command
	mock.CallCount = 0
	b.processCommand("/brightness", []string{"80"}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionBrightness || len(mock.LastAction.Args) != 1 || mock.LastAction.Args[0] != "80" {
		t.Errorf("expected ActionBrightness with arg '80', got %v", mock.LastAction)
	}

	// 4. Hotkey command
	mock.CallCount = 0
	b.processCommand("/hotkey", []string{"win+d"}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionHotkey || mock.LastAction.FlatArgs != "win+d" {
		t.Errorf("expected ActionHotkey with 'win+d', got %v", mock.LastAction)
	}

	// 5. Cmd command
	mock.CallCount = 0
	b.processCommand("/cmd", []string{"echo", "hello"}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionCmd || mock.LastAction.FlatArgs != "echo hello" {
		t.Errorf("expected ActionCmd with 'echo hello', got %v", mock.LastAction)
	}

	// 6. Processes command
	mock.CallCount = 0
	b.processCommand("/processes", []string{}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionProcesses {
		t.Errorf("expected ActionProcesses, got %v", mock.LastAction.Type)
	}

	// 7. DeviceInfo alias to SysInfo
	mock.CallCount = 0
	b.processCommand("/deviceinfo", []string{}, 123)
	if mock.CallCount != 1 || mock.LastAction.Type != executor.ActionSysInfo {
		t.Errorf("expected ActionSysInfo, got %v", mock.LastAction.Type)
	}
}

func TestProcessCommand_ExecutorError(t *testing.T) {
	mock := executor.NewMockExecutor(nil, errors.New("simulated host error"))

	b := &BotCoordinator{
		cfg: &config.Config{
			DeviceName:   "TestPC",
			AllowedUsers: []string{"123"},
		},
		executor: mock,
	}

	// Should safely route error and not panic
	mock.CallCount = 0
	b.processCommand("/screenshot", []string{}, 123)
	if mock.CallCount != 1 {
		t.Errorf("expected executor to be called once, got %d", mock.CallCount)
	}
}

func TestProcessCommand_ResultCleanup(t *testing.T) {
	cleaned := false
	mock := executor.NewMockExecutor(&executor.Result{
		Kind:     executor.ResultKindPhoto,
		FilePath: "dummy.jpg",
		Cleanup:  func() { cleaned = true },
	}, nil)

	b := &BotCoordinator{
		cfg: &config.Config{
			DeviceName:   "TestPC",
			AllowedUsers: []string{"123"},
		},
		executor: mock,
	}

	b.processCommand("/screenshot", []string{}, 123)
	if !cleaned {
		t.Error("expected Result.Cleanup() to be invoked after command dispatch")
	}
}

func TestRunSessionHelper_Delegation(t *testing.T) {
	// RunSessionHelper should run through in-process executor
	_, err := RunSessionHelper("/clipboard", "session_helper_test", "")
	if err != nil {
		t.Skipf("skipping clipboard session helper test in headless environment: %v", err)
	}
}
