package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"

	"github.com/caffeaun/farmhand/internal/installer/templates"
)

// CloudflareTunnel is the inputs required to wire up a tunnel configured via
// the partial-automation path: the user has already run `cloudflared tunnel
// login` and `cloudflared tunnel create <name>` themselves, so the
// credentials json exists under ~/.cloudflared/<id>.json.
type CloudflareTunnel struct {
	TunnelID   string // UUID printed by `cloudflared tunnel create`
	TunnelName string // human name passed to `cloudflared tunnel route dns`
	Hostname   string // the public hostname routed at the API
	ServerPort int    // farmhand's local server port
}

const (
	// systemCloudflaredDir is where the daemon variant of cloudflared looks
	// for config when run as root. Writing here avoids the user-vs-root home
	// resolution mismatch documented in docs/use-cases/03-macmini-with-ios.md.
	systemCloudflaredDir = "/etc/cloudflared"
)

// userCredentialsPath returns the path to the credentials json
// `cloudflared tunnel create` wrote under the current user's home.
func userCredentialsPath(tunnelID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cloudflared", tunnelID+".json")
}

// systemCredentialsPath returns where the installer copies the credentials
// json so the cloudflared system service can read it.
func systemCredentialsPath(tunnelID string) string {
	return filepath.Join(systemCloudflaredDir, tunnelID+".json")
}

// systemConfigPath is the cloudflared system service config file.
const systemConfigPath = systemCloudflaredDir + "/config.yml"

// cloudflaredRenderContext is the template payload for cloudflared.config.yml.tmpl.
type cloudflaredRenderContext struct {
	TunnelID        string
	CredentialsPath string
	Hostname        string
	ServerPort      int
}

// RenderCloudflareConfig returns the contents of /etc/cloudflared/config.yml.
func RenderCloudflareConfig(t CloudflareTunnel) ([]byte, error) {
	tmpl, err := template.ParseFS(templates.FS, "cloudflared.config.yml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse cloudflared template: %w", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, cloudflaredRenderContext{
		TunnelID:        t.TunnelID,
		CredentialsPath: systemCredentialsPath(t.TunnelID),
		Hostname:        t.Hostname,
		ServerPort:      t.ServerPort,
	})
	if err != nil {
		return nil, fmt.Errorf("render cloudflared config: %w", err)
	}
	return buf.Bytes(), nil
}

// InstallCloudflareConfig writes /etc/cloudflared/config.yml and copies the
// user's tunnel credentials json into /etc/cloudflared/ so that
// `sudo cloudflared service install` (run later by the user) can read them.
//
// Does NOT register a DNS route or install the service — that's the user's
// step, intentionally, because both require sudo + outward-facing changes.
// Returns the finishing commands the user should run.
//
// Requires root because it writes to /etc/cloudflared/.
func InstallCloudflareConfig(t CloudflareTunnel) (followUps []string, err error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("writing /etc/cloudflared/ requires root (re-run with sudo)")
	}
	if t.TunnelID == "" || t.Hostname == "" || t.ServerPort == 0 {
		return nil, fmt.Errorf("CloudflareTunnel requires TunnelID, Hostname, and ServerPort")
	}

	if err := os.MkdirAll(systemCloudflaredDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", systemCloudflaredDir, err)
	}

	// Copy credentials json out of the (user-running-sudo) home dir.
	srcCreds := userCredentialsPath(t.TunnelID)
	if _, err := os.Stat(srcCreds); err != nil {
		return nil, fmt.Errorf("credentials json not found at %s — run `cloudflared tunnel create %s` first", srcCreds, t.TunnelName)
	}
	if err := copyFileMode(srcCreds, systemCredentialsPath(t.TunnelID), 0o400); err != nil {
		return nil, err
	}

	// Write the system config.
	body, err := RenderCloudflareConfig(t)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(systemConfigPath, body, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", systemConfigPath, err)
	}

	// Tell the user what's left.
	name := t.TunnelName
	if name == "" {
		name = t.TunnelID
	}
	followUps = []string{
		fmt.Sprintf("cloudflared tunnel route dns %s %s", name, t.Hostname),
		"sudo cloudflared service install",
		fmt.Sprintf("curl -s https://%s/api/v1/health", t.Hostname),
	}
	return followUps, nil
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}
