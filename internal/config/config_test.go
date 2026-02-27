package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "farmhand-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

func TestDefaults(t *testing.T) {
	// Load from a path that doesn't exist — should use defaults only.
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("server defaults", func(t *testing.T) {
		if cfg.Server.Host != "0.0.0.0" {
			t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "0.0.0.0")
		}
		if cfg.Server.Port != 8080 {
			t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
		}
		if cfg.Server.AuthToken != "" {
			t.Errorf("Server.AuthToken = %q, want empty", cfg.Server.AuthToken)
		}
		if len(cfg.Server.CORSOrigins) != 1 || cfg.Server.CORSOrigins[0] != "*" {
			t.Errorf("Server.CORSOrigins = %v, want [\"*\"]", cfg.Server.CORSOrigins)
		}
		if cfg.Server.DevMode {
			t.Error("Server.DevMode = true, want false")
		}
	})

	t.Run("database defaults", func(t *testing.T) {
		if cfg.Database.Path != "farmhand.db" {
			t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, "farmhand.db")
		}
		if cfg.Database.RetentionDays != 30 {
			t.Errorf("Database.RetentionDays = %d, want 30", cfg.Database.RetentionDays)
		}
	})

	t.Run("devices defaults", func(t *testing.T) {
		if !cfg.Devices.AutoDiscover {
			t.Error("Devices.AutoDiscover = false, want true")
		}
		if cfg.Devices.PollIntervalSecs != 5 {
			t.Errorf("Devices.PollIntervalSecs = %d, want 5", cfg.Devices.PollIntervalSecs)
		}
		if cfg.Devices.MinBatteryPct != 20 {
			t.Errorf("Devices.MinBatteryPct = %d, want 20", cfg.Devices.MinBatteryPct)
		}
		if !cfg.Devices.CleanupBetweenRuns {
			t.Error("Devices.CleanupBetweenRuns = false, want true")
		}
		if !cfg.Devices.WakeBeforeTest {
			t.Error("Devices.WakeBeforeTest = false, want true")
		}
		if cfg.Devices.ADBPath != "adb" {
			t.Errorf("Devices.ADBPath = %q, want %q", cfg.Devices.ADBPath, "adb")
		}
	})

	t.Run("jobs defaults", func(t *testing.T) {
		if cfg.Jobs.DefaultTimeoutMin != 30 {
			t.Errorf("Jobs.DefaultTimeoutMin = %d, want 30", cfg.Jobs.DefaultTimeoutMin)
		}
		if cfg.Jobs.MaxConcurrentJobs != 3 {
			t.Errorf("Jobs.MaxConcurrentJobs = %d, want 3", cfg.Jobs.MaxConcurrentJobs)
		}
		if cfg.Jobs.ArtifactDir != "./artifacts" {
			t.Errorf("Jobs.ArtifactDir = %q, want %q", cfg.Jobs.ArtifactDir, "./artifacts")
		}
		if cfg.Jobs.ResultDir != "./results" {
			t.Errorf("Jobs.ResultDir = %q, want %q", cfg.Jobs.ResultDir, "./results")
		}
		if cfg.Jobs.LogDir != "./logs" {
			t.Errorf("Jobs.LogDir = %q, want %q", cfg.Jobs.LogDir, "./logs")
		}
		if cfg.Jobs.MaxArtifactSizeMB != 500 {
			t.Errorf("Jobs.MaxArtifactSizeMB = %d, want 500", cfg.Jobs.MaxArtifactSizeMB)
		}
	})

	t.Run("notifications defaults", func(t *testing.T) {
		if cfg.Notifications.WebhookURL != "" {
			t.Errorf("Notifications.WebhookURL = %q, want empty", cfg.Notifications.WebhookURL)
		}
		want := []string{"failure", "completion"}
		if len(cfg.Notifications.NotifyOn) != len(want) {
			t.Errorf("Notifications.NotifyOn = %v, want %v", cfg.Notifications.NotifyOn, want)
		} else {
			for i, v := range want {
				if cfg.Notifications.NotifyOn[i] != v {
					t.Errorf("Notifications.NotifyOn[%d] = %q, want %q", i, cfg.Notifications.NotifyOn[i], v)
				}
			}
		}
	})
}

func TestYAMLOverride(t *testing.T) {
	yaml := `
server:
  host: "127.0.0.1"
  port: 9090
  auth_token: "secret"
  dev_mode: true
database:
  path: "/data/farmhand.db"
  retention_days: 60
devices:
  poll_interval_seconds: 10
  adb_path: "/usr/local/bin/adb"
jobs:
  default_timeout_minutes: 45
  max_concurrent_jobs: 5
  artifact_storage_path: "/tmp/artifacts"
  log_dir: "/var/log/farmhand"
notifications:
  webhook_url: "https://hooks.example.com/notify"
  notify_on:
    - failure
`
	path := writeTemp(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Server.AuthToken != "secret" {
		t.Errorf("Server.AuthToken = %q, want secret", cfg.Server.AuthToken)
	}
	if !cfg.Server.DevMode {
		t.Error("Server.DevMode = false, want true")
	}
	if cfg.Database.Path != "/data/farmhand.db" {
		t.Errorf("Database.Path = %q, want /data/farmhand.db", cfg.Database.Path)
	}
	if cfg.Database.RetentionDays != 60 {
		t.Errorf("Database.RetentionDays = %d, want 60", cfg.Database.RetentionDays)
	}
	if cfg.Devices.PollIntervalSecs != 10 {
		t.Errorf("Devices.PollIntervalSecs = %d, want 10", cfg.Devices.PollIntervalSecs)
	}
	if cfg.Devices.ADBPath != "/usr/local/bin/adb" {
		t.Errorf("Devices.ADBPath = %q, want /usr/local/bin/adb", cfg.Devices.ADBPath)
	}
	if cfg.Jobs.DefaultTimeoutMin != 45 {
		t.Errorf("Jobs.DefaultTimeoutMin = %d, want 45", cfg.Jobs.DefaultTimeoutMin)
	}
	if cfg.Jobs.MaxConcurrentJobs != 5 {
		t.Errorf("Jobs.MaxConcurrentJobs = %d, want 5", cfg.Jobs.MaxConcurrentJobs)
	}
	if cfg.Jobs.ArtifactDir != "/tmp/artifacts" {
		t.Errorf("Jobs.ArtifactDir = %q, want /tmp/artifacts", cfg.Jobs.ArtifactDir)
	}
	if cfg.Jobs.LogDir != "/var/log/farmhand" {
		t.Errorf("Jobs.LogDir = %q, want /var/log/farmhand", cfg.Jobs.LogDir)
	}
	if cfg.Notifications.WebhookURL != "https://hooks.example.com/notify" {
		t.Errorf("Notifications.WebhookURL = %q", cfg.Notifications.WebhookURL)
	}
	if len(cfg.Notifications.NotifyOn) != 1 || cfg.Notifications.NotifyOn[0] != "failure" {
		t.Errorf("Notifications.NotifyOn = %v, want [failure]", cfg.Notifications.NotifyOn)
	}
}

func TestEnvOverrideTakesPrecedenceOverYAML(t *testing.T) {
	yaml := `
server:
  port: 9090
  host: "127.0.0.1"
database:
  path: "from-yaml.db"
`
	path := writeTemp(t, yaml)

	t.Setenv("FARMHAND_PORT", "7777")
	t.Setenv("FARMHAND_HOST", "192.168.1.1")
	t.Setenv("FARMHAND_DB_PATH", "from-env.db")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 7777 {
		t.Errorf("Server.Port = %d, want 7777 (env should override YAML)", cfg.Server.Port)
	}
	if cfg.Server.Host != "192.168.1.1" {
		t.Errorf("Server.Host = %q, want 192.168.1.1 (env should override YAML)", cfg.Server.Host)
	}
	if cfg.Database.Path != "from-env.db" {
		t.Errorf("Database.Path = %q, want from-env.db (env should override YAML)", cfg.Database.Path)
	}
}

func TestMissingYAMLUsesDefaults(t *testing.T) {
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cfg, err := Load(nonexistent)
	if err != nil {
		t.Fatalf("Load with missing file returned error: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Database.Path != "farmhand.db" {
		t.Errorf("Database.Path = %q, want farmhand.db", cfg.Database.Path)
	}
}

func TestInvalidYAMLReturnsError(t *testing.T) {
	path := writeTemp(t, "server:\n  port: [not an int\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	if !containsAny(err.Error(), "parse YAML", "yaml") {
		t.Errorf("error should mention YAML parsing, got: %v", err)
	}
}

func TestInvalidEnvVarPortReturnsError(t *testing.T) {
	t.Setenv("FARMHAND_PORT", "abc")
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for invalid FARMHAND_PORT, got nil")
	}
	if !containsAny(err.Error(), "FARMHAND_PORT", "integer") {
		t.Errorf("error should mention FARMHAND_PORT and invalid value, got: %v", err)
	}
}

func TestInvalidEnvVarDevModeReturnsError(t *testing.T) {
	t.Setenv("FARMHAND_DEV_MODE", "notabool")
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for invalid FARMHAND_DEV_MODE, got nil")
	}
	if !containsAny(err.Error(), "FARMHAND_DEV_MODE", "boolean") {
		t.Errorf("error should mention FARMHAND_DEV_MODE and invalid value, got: %v", err)
	}
}

func TestInvalidEnvVarPollIntervalReturnsError(t *testing.T) {
	t.Setenv("FARMHAND_DEVICE_POLL_INTERVAL", "xyz")
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for invalid FARMHAND_DEVICE_POLL_INTERVAL, got nil")
	}
	if !containsAny(err.Error(), "FARMHAND_DEVICE_POLL_INTERVAL", "integer") {
		t.Errorf("error should mention FARMHAND_DEVICE_POLL_INTERVAL and invalid value, got: %v", err)
	}
}

func TestAllEnvVarMappings(t *testing.T) {
	t.Setenv("FARMHAND_HOST", "10.0.0.1")
	t.Setenv("FARMHAND_PORT", "1234")
	t.Setenv("FARMHAND_AUTH_TOKEN", "mytoken")
	t.Setenv("FARMHAND_DEV_MODE", "true")
	t.Setenv("FARMHAND_DB_PATH", "custom.db")
	t.Setenv("FARMHAND_LOG_DIR", "/custom/logs")
	t.Setenv("FARMHAND_ARTIFACT_DIR", "/custom/artifacts")
	t.Setenv("FARMHAND_DEVICE_POLL_INTERVAL", "15")
	t.Setenv("FARMHAND_WEBHOOK_URL", "https://example.com/hook")
	t.Setenv("FARMHAND_ADB_PATH", "/custom/adb")

	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Server.Host", cfg.Server.Host, "10.0.0.1"},
		{"Server.Port", cfg.Server.Port, 1234},
		{"Server.AuthToken", cfg.Server.AuthToken, "mytoken"},
		{"Server.DevMode", cfg.Server.DevMode, true},
		{"Database.Path", cfg.Database.Path, "custom.db"},
		{"Jobs.LogDir", cfg.Jobs.LogDir, "/custom/logs"},
		{"Jobs.ArtifactDir", cfg.Jobs.ArtifactDir, "/custom/artifacts"},
		{"Devices.PollIntervalSecs", cfg.Devices.PollIntervalSecs, 15},
		{"Notifications.WebhookURL", cfg.Notifications.WebhookURL, "https://example.com/hook"},
		{"Devices.ADBPath", cfg.Devices.ADBPath, "/custom/adb"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	sl := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(sl, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
