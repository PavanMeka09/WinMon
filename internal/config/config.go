package config

import (
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

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

func isValidToken(token string) bool {
	t := strings.TrimSpace(token)
	return t != "" && t != "YOUR_DISCORD_BOT_TOKEN" && t != "YOUR_TELEGRAM_BOT_TOKEN"
}

func LoadConfig() (*Config, error) {
	var data []byte

	// 1. Try external config.json in executable directory
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		extPath := filepath.Join(exeDir, "config.json")
		if bytes, err := os.ReadFile(extPath); err == nil {
			var tempCfg Config
			if err := json.Unmarshal(bytes, &tempCfg); err == nil && isValidToken(tempCfg.BotToken) {
				data = bytes
			}
		}
	}

	// 2. Try external config.json in working directory
	if len(data) == 0 {
		if bytes, err := os.ReadFile("config.json"); err == nil {
			var tempCfg Config
			if err := json.Unmarshal(bytes, &tempCfg); err == nil && isValidToken(tempCfg.BotToken) {
				data = bytes
			}
		}
	}

	// 3. Try embedded FS config.json
	if len(data) == 0 {
		if bytes, err := configFS.ReadFile("config.json"); err == nil {
			var tempCfg Config
			if err := json.Unmarshal(bytes, &tempCfg); err == nil && isValidToken(tempCfg.BotToken) {
				data = bytes
			}
		}
	}

	// 4. Fallback to embedded template
	if len(data) == 0 {
		if bytes, err := configFS.ReadFile("config.json.template"); err == nil {
			data = bytes
		}
	}

	if len(data) == 0 {
		return nil, errors.New("no configuration data found")
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if !isValidToken(cfg.BotToken) {
		return nil, errors.New("invalid or missing bot_token in configuration")
	}

	// Dynamically override DeviceName and DeviceID
	cfg.DeviceName = device.GetComputerName()
	cfg.DeviceID = device.GetComputerUUID()

	return &cfg, nil
}

