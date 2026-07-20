package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Fallback(t *testing.T) {
	// Create a temporary config file on disk
	tempDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tempConfigPath := filepath.Join(tempDir, "config.json")
	content := `{
		"bot_token": "TEST_TOKEN_FROM_DISK",
		"allowed_users": [123]
	}`
	if err := os.WriteFile(tempConfigPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(tempConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Since the embedded config has an empty token (from config.json.template),
	// it must fall back to loading from disk.
	if cfg.BotToken != "TEST_TOKEN_FROM_DISK" {
		t.Errorf("expected BotToken 'TEST_TOKEN_FROM_DISK', got '%s'", cfg.BotToken)
	}
}

func TestLoadConfig_Embedded(t *testing.T) {
	// Skip the test if config.json is not embedded (i.e. not present in the package FS)
	if _, err := configFS.ReadFile("config.json"); err != nil {
		t.Skip("Skipping TestLoadConfig_Embedded: internal/config/config.json is not present to test embedded config loading.")
	}

	// Since config.json was copied to the package directory, it should embed it.
	// We pass a non-existent path. If it succeeds, it successfully loaded the embedded config.
	cfg, err := LoadConfig("non_existent_config_file_path.json")
	if err != nil {
		t.Fatalf("LoadConfig failed to load embedded config: %v", err)
	}

	if cfg.BotToken == "" || cfg.BotToken == "YOUR_TELEGRAM_BOT_TOKEN" {
		t.Errorf("loaded bot token is empty or placeholder: '%s'", cfg.BotToken)
	}
}
