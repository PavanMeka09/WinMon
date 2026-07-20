package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigOverrides(t *testing.T) {
	// Create a temporary directory for testing config
	tmpDir, err := os.MkdirTemp("", "winmon-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configJSON := `{
		"bot_token": "test-token",
		"device_id": "original-id",
		"device_name": "original-name"
	}`

	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	// Load config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify bot token is loaded correctly
	if cfg.BotToken != "test-token" {
		t.Errorf("expected BotToken to be 'test-token', got %q", cfg.BotToken)
	}

	// Verify DeviceName is dynamically set to the computer name (hostname)
	expectedName, err := os.Hostname()
	if err == nil && expectedName != "" {
		if cfg.DeviceName != expectedName {
			t.Errorf("expected DeviceName to be overridden to hostname %q, got %q", expectedName, cfg.DeviceName)
		}
	} else if cfg.DeviceName == "" {
		t.Errorf("expected DeviceName to be set, but it was empty")
	}

	// Verify DeviceID is dynamically set to computer UUID (which is lowercase)
	if cfg.DeviceID == "" || cfg.DeviceID == "original-id" {
		t.Errorf("expected DeviceID to be overridden to hardware UUID, got %q", cfg.DeviceID)
	}

	// Verify UUID format (contains hyphens and is lower-case) if it's not "unknown-uuid"
	if cfg.DeviceID != "unknown-uuid" {
		if strings.ToUpper(cfg.DeviceID) == cfg.DeviceID && cfg.DeviceID != "" {
			t.Errorf("expected DeviceID to be lowercase, got %q", cfg.DeviceID)
		}
	}
}
