package config

import (
	"testing"
)

func TestLoadConfig_Embedded(t *testing.T) {
	// Skip the test if config.json is not embedded (i.e. not present in the package FS)
	if _, err := configFS.ReadFile("config.json"); err != nil {
		t.Skip("Skipping TestLoadConfig_Embedded: internal/config/config.json is not present to test embedded config loading.")
	}

	// Since config.json was copied to the package directory, it should embed it.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed to load embedded config: %v", err)
	}

	if cfg.BotToken == "" || cfg.BotToken == "YOUR_TELEGRAM_BOT_TOKEN" {
		t.Errorf("loaded bot token is empty or placeholder: '%s'", cfg.BotToken)
	}
}
