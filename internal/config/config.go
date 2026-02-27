// Package config loads and validates application configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Server        ServerConfig        `yaml:"server" json:"server"`
	Database      DatabaseConfig      `yaml:"database" json:"database"`
	Devices       DevicesConfig       `yaml:"devices" json:"devices"`
	Jobs          JobsConfig          `yaml:"jobs" json:"jobs"`
	Notifications NotificationsConfig `yaml:"notifications" json:"notifications"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host        string   `yaml:"host" json:"host"`
	Port        int      `yaml:"port" json:"port"`
	AuthToken   string   `yaml:"auth_token" json:"auth_token"`
	CORSOrigins []string `yaml:"cors_origins" json:"cors_origins"`
	DevMode     bool     `yaml:"dev_mode" json:"dev_mode"`
}

// DatabaseConfig holds database settings.
type DatabaseConfig struct {
	Path          string `yaml:"path" json:"path"`
	RetentionDays int    `yaml:"retention_days" json:"retention_days"`
}

// DevicesConfig holds Android device management settings.
type DevicesConfig struct {
	AutoDiscover       bool   `yaml:"auto_discover" json:"auto_discover"`
	PollIntervalSecs   int    `yaml:"poll_interval_seconds" json:"poll_interval_seconds"`
	MinBatteryPct      int    `yaml:"min_battery_percent" json:"min_battery_percent"`
	CleanupBetweenRuns bool   `yaml:"cleanup_between_runs" json:"cleanup_between_runs"`
	WakeBeforeTest     bool   `yaml:"wake_before_test" json:"wake_before_test"`
	ADBPath            string `yaml:"adb_path" json:"adb_path"`
}

// JobsConfig holds job execution settings.
type JobsConfig struct {
	DefaultTimeoutMin int    `yaml:"default_timeout_minutes" json:"default_timeout_minutes"`
	MaxConcurrentJobs int    `yaml:"max_concurrent_jobs" json:"max_concurrent_jobs"`
	ArtifactDir       string `yaml:"artifact_storage_path" json:"artifact_storage_path"`
	ResultDir         string `yaml:"result_storage_path" json:"result_storage_path"`
	LogDir            string `yaml:"log_dir" json:"log_dir"`
	MaxArtifactSizeMB int    `yaml:"max_artifact_size_mb" json:"max_artifact_size_mb"`
}

// NotificationsConfig holds webhook notification settings.
type NotificationsConfig struct {
	WebhookURL string   `yaml:"webhook_url" json:"webhook_url"`
	NotifyOn   []string `yaml:"notify_on" json:"notify_on"`
}

// defaults returns a Config populated with all default values.
func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host:        "0.0.0.0",
			Port:        8080,
			AuthToken:   "",
			CORSOrigins: []string{"*"},
			DevMode:     false,
		},
		Database: DatabaseConfig{
			Path:          "farmhand.db",
			RetentionDays: 30,
		},
		Devices: DevicesConfig{
			AutoDiscover:       true,
			PollIntervalSecs:   5,
			MinBatteryPct:      20,
			CleanupBetweenRuns: true,
			WakeBeforeTest:     true,
			ADBPath:            "adb",
		},
		Jobs: JobsConfig{
			DefaultTimeoutMin: 30,
			MaxConcurrentJobs: 3,
			ArtifactDir:       "./artifacts",
			ResultDir:         "./results",
			LogDir:            "./logs",
			MaxArtifactSizeMB: 500,
		},
		Notifications: NotificationsConfig{
			WebhookURL: "",
			NotifyOn:   []string{"failure", "completion"},
		},
	}
}

// Load reads configuration from the YAML file at path, applies defaults for
// missing fields, and then overrides values from FARMHAND_* environment
// variables. If the YAML file does not exist, only defaults and env vars are
// used. Invalid YAML or invalid env var values return a descriptive error.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: read file %q: %w", path, err)
		}
		// File does not exist — use defaults + env vars only.
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse YAML %q: %w", path, err)
		}
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyEnv overrides config fields from FARMHAND_* environment variables.
func applyEnv(cfg *Config) error {
	for _, env := range os.Environ() {
		key, val, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		switch key {
		case "FARMHAND_HOST":
			cfg.Server.Host = val
		case "FARMHAND_PORT":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("config: env FARMHAND_PORT=%q is not a valid integer", val)
			}
			cfg.Server.Port = n
		case "FARMHAND_AUTH_TOKEN":
			cfg.Server.AuthToken = val
		case "FARMHAND_DEV_MODE":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("config: env FARMHAND_DEV_MODE=%q is not a valid boolean", val)
			}
			cfg.Server.DevMode = b
		case "FARMHAND_DB_PATH":
			cfg.Database.Path = val
		case "FARMHAND_LOG_DIR":
			cfg.Jobs.LogDir = val
		case "FARMHAND_ARTIFACT_DIR":
			cfg.Jobs.ArtifactDir = val
		case "FARMHAND_DEVICE_POLL_INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("config: env FARMHAND_DEVICE_POLL_INTERVAL=%q is not a valid integer", val)
			}
			cfg.Devices.PollIntervalSecs = n
		case "FARMHAND_WEBHOOK_URL":
			cfg.Notifications.WebhookURL = val
		case "FARMHAND_ADB_PATH":
			cfg.Devices.ADBPath = val
		}
	}
	return nil
}
