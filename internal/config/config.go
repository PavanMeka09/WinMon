package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
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
