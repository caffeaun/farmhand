package installer

import (
	"strings"
	"testing"
)

func TestGenerateAuthToken(t *testing.T) {
	a, err := GenerateAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two calls produced same token: %s", a)
	}
	// 32 bytes -> RawURLEncoding -> ceil(32*4/3) = 43 chars.
	if len(a) != 43 {
		t.Fatalf("token len = %d, want 43 (%s)", len(a), a)
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		os, arch, want string
	}{
		{"linux", "amd64", "farmhand-linux-amd64"},
		{"linux", "arm64", "farmhand-linux-arm64"},
		{"darwin", "amd64", "farmhand-darwin-amd64"},
		{"darwin", "arm64", "farmhand-darwin-arm64"},
	}
	for _, c := range cases {
		got := Platform{OS: c.os, Arch: c.arch}.AssetName()
		if got != c.want {
			t.Errorf("AssetName(%s,%s) = %s, want %s", c.os, c.arch, got, c.want)
		}
	}
}

func TestDerivedLayout(t *testing.T) {
	l := DerivedLayout("/opt/farmhand")
	wants := map[string]string{
		"InstallDir":   "/opt/farmhand",
		"BinaryPath":   "/usr/local/bin/farmhand",
		"ConfigPath":   "/opt/farmhand/farmhand.yaml",
		"DatabasePath": "/opt/farmhand/farmhand.db",
		"ArtifactDir":  "/opt/farmhand/artifacts",
		"ResultDir":    "/opt/farmhand/results",
		"LogDir":       "/opt/farmhand/logs",
	}
	got := map[string]string{
		"InstallDir":   l.InstallDir,
		"BinaryPath":   l.BinaryPath,
		"ConfigPath":   l.ConfigPath,
		"DatabasePath": l.DatabasePath,
		"ArtifactDir":  l.ArtifactDir,
		"ResultDir":    l.ResultDir,
		"LogDir":       l.LogDir,
	}
	for k, want := range wants {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestRenderConfig(t *testing.T) {
	layout := DerivedLayout("/opt/farmhand")
	cfg := DefaultConfig(layout, 8080, "127.0.0.1", "test-token-xyz", false)
	body, err := RenderConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)

	// Key invariants — these are what the tunnel-based deployments rely on.
	checks := []string{
		`host: "127.0.0.1"`,
		`port: 8080`,
		`auth_token: "test-token-xyz"`,
		`path: "/opt/farmhand/farmhand.db"`,
		`artifact_storage_path: "/opt/farmhand/artifacts"`,
		`model: "MiniMax-M3"`,
	}
	for _, c := range checks {
		if !strings.Contains(s, c) {
			t.Errorf("rendered config missing %q. Output:\n%s", c, s)
		}
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	layout := DerivedLayout("/opt/farmhand")
	body, err := RenderSystemdUnit(layout)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, c := range []string{
		"User=farmhand",
		"WorkingDirectory=/opt/farmhand",
		"ExecStart=/usr/local/bin/farmhand serve --config /opt/farmhand/farmhand.yaml",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(s, c) {
			t.Errorf("systemd unit missing %q. Output:\n%s", c, s)
		}
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	layout := DerivedLayout("/opt/farmhand")
	body, err := RenderLaunchdPlist(layout)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, c := range []string{
		`<string>io.kanolab.farmhand</string>`,
		`<string>/usr/local/bin/farmhand</string>`,
		`<string>/opt/farmhand/farmhand.yaml</string>`,
		`<string>/opt/farmhand/logs/stdout.log</string>`,
	} {
		if !strings.Contains(s, c) {
			t.Errorf("launchd plist missing %q. Output:\n%s", c, s)
		}
	}
}

func TestRenderCloudflareConfig(t *testing.T) {
	body, err := RenderCloudflareConfig(CloudflareTunnel{
		TunnelID:   "abc-123",
		TunnelName: "kanolab-ubuntu",
		Hostname:   "devices.example.com",
		ServerPort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, c := range []string{
		"tunnel: abc-123",
		"credentials-file: /etc/cloudflared/abc-123.json",
		"hostname: devices.example.com",
		"path: /api/.*",
		"service: http://127.0.0.1:8080",
		"service: http_status:404",
	} {
		if !strings.Contains(s, c) {
			t.Errorf("cloudflared config missing %q. Output:\n%s", c, s)
		}
	}
}

func TestPrompterAssumeYes(t *testing.T) {
	p := NewPrompter(strings.NewReader(""), &strings.Builder{}, true)
	got, err := p.Ask("HTTP port", "8080")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8080" {
		t.Errorf("Ask(assume) = %q, want default 8080", got)
	}
	ok, err := p.Confirm("Continue?", true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("Confirm(assume, defaultYes) = false")
	}
	ok, err = p.Confirm("Wipe everything?", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("Confirm(assume, defaultNo) = true")
	}
}

func TestPrompterReadsLine(t *testing.T) {
	p := NewPrompter(strings.NewReader("9999\n"), &strings.Builder{}, false)
	got, err := p.Ask("port", "8080")
	if err != nil {
		t.Fatal(err)
	}
	if got != "9999" {
		t.Errorf("Ask = %q, want 9999", got)
	}
}
