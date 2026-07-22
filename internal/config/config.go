package config

import (
	"embed"
	"encoding/json"
	"errors"

	"winmon/internal/device"
)

//go:embed config.json*
var configFS embed.FS

type Config struct {
	BotToken              string   `json:"bot_token"`
	AllowedUsers          []string `json:"allowed_users"`
	DeviceID              string   `json:"device_id"`
	DeviceName            string   `json:"device_name"`
	Group                 string   `json:"group"`
	Version               string   `json:"version"`
	MaxUploadSizeMB       int64    `json:"max_upload_size_mb"`
	CommandTimeoutSeconds int      `json:"command_timeout_seconds"`
}

func LoadConfig() (*Config, error) {
	// 1. Try embedded FS (config.json first, then config.json.template)
	var data []byte
	var loadedFromEmbedded bool

	if bytes, err := configFS.ReadFile("config.json"); err == nil {
		var tempCfg Config
		if err := json.Unmarshal(bytes, &tempCfg); err == nil {
			if tempCfg.BotToken != "" && tempCfg.BotToken != "YOUR_DISCORD_BOT_TOKEN" && tempCfg.BotToken != "YOUR_TELEGRAM_BOT_TOKEN" {
				data = bytes
				loadedFromEmbedded = true
			}
		}
	}

	if !loadedFromEmbedded {
		if bytes, err := configFS.ReadFile("config.json.template"); err == nil {
			data = bytes
		}
	}

	if len(data) == 0 {
		return nil, errors.New("no configuration data found in embedded filesystem")
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// If the loaded configuration from template has no token, it means we have no valid config at all.
	if cfg.BotToken == "" || cfg.BotToken == "YOUR_DISCORD_BOT_TOKEN" || cfg.BotToken == "YOUR_TELEGRAM_BOT_TOKEN" {
		return nil, errors.New("no valid configuration found embedded")
	}

	// Dynamically override DeviceName and DeviceID
	cfg.DeviceName = device.GetComputerName()
	cfg.DeviceID = device.GetComputerUUID()

	return &cfg, nil
}
