package config

import (
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"winmon/internal/device"
)

//go:embed config.json*
var configFS embed.FS

type Config struct {
	BotToken              string   `json:"bot_token"`
	AllowedUsers          []int64  `json:"allowed_users"`
	DeviceID              string   `json:"device_id"`
	DeviceName            string   `json:"device_name"`
	Group                 string   `json:"group"`
	Version               string   `json:"version"`
	MaxUploadSizeMB       int64    `json:"max_upload_size_mb"`
	CommandTimeoutSeconds int      `json:"command_timeout_seconds"`
	AllowedCmds           []string `json:"allowed_cmds"`
}

func LoadConfig(path string) (*Config, error) {
	// 1. Try to load from disk first
	if data, err := os.ReadFile(path); err == nil {
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err == nil {
			// Dynamically override DeviceName and DeviceID
			cfg.DeviceName = device.GetComputerName()
			cfg.DeviceID = device.GetComputerUUID()
			return &cfg, nil
		}
	}

	// 2. If disk loading fails or is empty, try embedded FS (config.json first, then config.json.template)
	var data []byte
	var loadedFromEmbedded bool

	if bytes, err := configFS.ReadFile("config.json"); err == nil {
		var tempCfg Config
		if err := json.Unmarshal(bytes, &tempCfg); err == nil {
			if tempCfg.BotToken != "" && tempCfg.BotToken != "YOUR_TELEGRAM_BOT_TOKEN" {
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
		return nil, os.ErrNotExist
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// If the loaded configuration from template has no token, it means we have no valid config at all.
	if cfg.BotToken == "" || cfg.BotToken == "YOUR_TELEGRAM_BOT_TOKEN" {
		return nil, errors.New("no valid configuration found on disk or embedded")
	}

	// Dynamically override DeviceName and DeviceID
	cfg.DeviceName = device.GetComputerName()
	cfg.DeviceID = device.GetComputerUUID()

	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func GetDefaultConfigPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(filepath.Dir(exePath), "config.json")
}
