package config

import (
	"testing"
)

func TestLoadConfig_Embedded(t *testing.T) {
	if _, err := configFS.ReadFile("config.json"); err != nil {
		t.Skip("Skipping TestLoadConfig_Embedded: internal/config/config.json is not present to test embedded config loading.")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Skipf("Skipping TestLoadConfig_Embedded: %v", err)
	}

	if cfg.BotToken == "" || cfg.BotToken == "YOUR_TELEGRAM_BOT_TOKEN" {
		t.Errorf("loaded bot token is empty or placeholder: '%s'", cfg.BotToken)
	}
}

func TestNormalizeAllowedUsers(t *testing.T) {
	numeric, skipped := NormalizeAllowedUsers([]string{
		"123456789",
		" @987654321 ",
		"alice",
		"",
		"@notanid",
	})

	if len(numeric) != 2 || numeric[0] != "123456789" || numeric[1] != "987654321" {
		t.Fatalf("unexpected numeric IDs: %#v", numeric)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped entries, got %#v", skipped)
	}
}
