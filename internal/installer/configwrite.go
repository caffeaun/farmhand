package installer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/caffeaun/farmhand/internal/config"
	"github.com/caffeaun/farmhand/internal/installer/templates"
)

// configRenderContext is the template payload for farmhand.yaml.tmpl. It
// wraps the runtime Config plus a generation timestamp so the rendered file
// has a self-documenting header.
type configRenderContext struct {
	config.Config
	GeneratedAt string
}

// RenderConfig produces the contents of farmhand.yaml from cfg. It does not
// touch the filesystem — caller decides where to write.
func RenderConfig(cfg config.Config) ([]byte, error) {
	tmpl, err := template.ParseFS(templates.FS, "farmhand.yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse farmhand.yaml template: %w", err)
	}
	var buf bytes.Buffer
	ctx := configRenderContext{
		Config:      cfg,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("render farmhand.yaml: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteConfig renders the config and writes it to path atomically (write to
// path.tmp, fsync, rename). Returns ErrExist if the file already exists and
// overwrite is false.
func WriteConfig(path string, cfg config.Config, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (pass overwrite=true to replace)", path)
		}
	}
	body, err := RenderConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// DefaultConfig builds a Config tailored for an install at `layout`, with a
// freshly-generated auth token (or empty if devMode). It mirrors the
// "production-ready" defaults the docs prescribe: bind 127.0.0.1 for
// Cloudflare-tunnel deployments, paths anchored at the install dir, no
// webhook configured.
func DefaultConfig(layout Layout, port int, host, authToken string, devMode bool) config.Config {
	return config.Config{
		Server: config.ServerConfig{
			Host:        host,
			Port:        port,
			AuthToken:   authToken,
			CORSOrigins: []string{"*"},
			DevMode:     devMode,
		},
		Database: config.DatabaseConfig{
			Path:          layout.DatabasePath,
			RetentionDays: 30,
		},
		Devices: config.DevicesConfig{
			AutoDiscover:       true,
			PollIntervalSecs:   5,
			MinBatteryPct:      20,
			CleanupBetweenRuns: true,
			WakeBeforeTest:     true,
			ADBPath:            "adb",
		},
		Jobs: config.JobsConfig{
			DefaultTimeoutMin: 30,
			MaxConcurrentJobs: 3,
			ArtifactDir:       layout.ArtifactDir,
			ResultDir:         layout.ResultDir,
			LogDir:            layout.LogDir,
			MaxArtifactSizeMB: 500,
		},
		Notifications: config.NotificationsConfig{
			WebhookURL: "",
			NotifyOn:   []string{"failure", "completion"},
		},
		Vision: config.VisionConfig{
			Provider:   "minimax",
			APIKeyEnv:  "MINIMAX_API_KEY",
			BaseURL:    "https://api.minimax.io/v1",
			Model:      "MiniMax-M3",
			TimeoutSec: 15,
			Detail:     "high",
		},
	}
}
