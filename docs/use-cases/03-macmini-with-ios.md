# Mac mini + iOS Devices

Install guide: Mac mini (Apple Silicon) running FarmHand as a launchd agent for iOS device testing, exposed via Cloudflare Tunnel. Uses a pre-built GitHub release binary — no Go toolchain required on the Mac mini.

**Key principle**: The web dashboard is only accessible on `localhost`. Only the API (`/api/v1/*`) is exposed through Cloudflare, protected by an auth token.

**Related docs**: [API Reference](../API_REFERENCES.md) | [README](../../README.md) | [Ubuntu use case](01-ubuntu-with-flutter-android.md)

---

## Table of Contents

- [Quick Reference](#quick-reference)
- [1. Prerequisites](#1-prerequisites)
- [2. Download the release binary](#2-download-the-release-binary)
- [3. Install layout](#3-install-layout)
- [4. Configure FarmHand](#4-configure-farmhand)
- [5. launchd daemon](#5-launchd-daemon)
- [6. Cloudflare Tunnel](#6-cloudflare-tunnel)
- [7. Verify end-to-end](#7-verify-end-to-end)
- [Updating FarmHand](#updating-farmhand)
- [Troubleshooting](#troubleshooting)

---

## Quick Reference

| Component | Purpose |
|-----------|---------|
| FarmHand binary | Device farm server (`/opt/farmhand/farmhand`) |
| `farmhand.yaml` | Server config — auth token, DB path, device settings |
| launchd | Keeps FarmHand running as a background service |
| Cloudflare Tunnel | Exposes only `/api/v1/*` to the internet (no open ports) |
| Xcode (full) | Required for `xcrun xctrace list devices` (Instruments) |
| Homebrew | Installs `cloudflared` and the `gh` CLI |

> **Note on iOS tooling**: The iOS bridge (`internal/device/ios.go`) shells out to `xcrun xctrace list devices`. `xctrace` ships with Instruments inside **full Xcode.app** — the standalone Command Line Tools are not enough.

---

## 1. Prerequisites

Run these on the Mac mini.

```bash
# Xcode Command Line Tools (needed for xcrun)
xcode-select --install

# Full Xcode is required for xctrace — install from the Mac App Store, then:
sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
sudo xcodebuild -license accept

# Verify
xcrun xctrace list devices

# Homebrew (skip if already installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Cloudflare tunnel + gh CLI (gh makes downloading the release easy)
brew install cloudflared gh
```

---

## 2. Download the release binary

Pre-built binaries are published on every GitHub release. For an Apple Silicon Mac mini, you want `farmhand-darwin-arm64`.

```bash
gh auth login                  # one-time, GitHub login
mkdir -p ~/farmhand-download && cd ~/farmhand-download

# Pin a specific version (recommended) — see https://github.com/caffeaun/farmhand/releases
gh release download v0.3.3 --repo caffeaun/farmhand --pattern 'farmhand-darwin-arm64'

# Or grab the latest
# gh release download --repo caffeaun/farmhand --pattern 'farmhand-darwin-arm64'

chmod +x farmhand-darwin-arm64
./farmhand-darwin-arm64 --help
```

---

## 3. Install layout

```bash
sudo mkdir -p /opt/farmhand/{artifacts,results,logs}
sudo mv farmhand-darwin-arm64 /opt/farmhand/farmhand
sudo chown -R "$(whoami)" /opt/farmhand
```

---

## 4. Configure FarmHand

Generate an auth token and write the config file:

```bash
TOKEN=$(python3 -c "import secrets; print(secrets.token_urlsafe(32))")
echo "$TOKEN"     # save this — you'll need it for API calls

cat > /opt/farmhand/farmhand.yaml <<EOF
server:
  host: "127.0.0.1"          # local only; Cloudflare exposes the API
  port: 8080
  auth_token: "$TOKEN"
  cors_origins: ["*"]
  dev_mode: false

database:
  path: "/opt/farmhand/farmhand.db"
  retention_days: 30

devices:
  auto_discover: true
  poll_interval_seconds: 5

jobs:
  default_timeout_minutes: 30
  max_concurrent_jobs: 3
  artifact_storage_path: "/opt/farmhand/artifacts"
  result_storage_path: "/opt/farmhand/results"
  log_dir: "/opt/farmhand/logs"
  max_artifact_size_mb: 500

notifications:
  webhook_url: ""
EOF
```

Optional sanity check before daemonizing:

```bash
cd /opt/farmhand && ./farmhand serve --config farmhand.yaml
# Ctrl-C after you confirm http://localhost:8080 loads the dashboard
```

See [farmhand.example.yaml](../../farmhand.example.yaml) for all available options.

---

## 5. launchd daemon

Use a **LaunchAgent** (not a system Daemon) so the service runs under your user account — `xcrun` and physical-device entitlements work cleanly that way.

```bash
cat > ~/Library/LaunchAgents/io.kanolab.farmhand.plist <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>io.kanolab.farmhand</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/farmhand/farmhand</string>
        <string>serve</string>
        <string>--config</string>
        <string>/opt/farmhand/farmhand.yaml</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>WorkingDirectory</key><string>/opt/farmhand</string>
    <key>StandardOutPath</key><string>/opt/farmhand/logs/stdout.log</string>
    <key>StandardErrorPath</key><string>/opt/farmhand/logs/stderr.log</string>
</dict>
</plist>
EOF

launchctl load ~/Library/LaunchAgents/io.kanolab.farmhand.plist
launchctl list | grep farmhand        # should show a PID
curl http://localhost:8080/api/v1/health
```

To reload after a config or binary change:

```bash
launchctl unload ~/Library/LaunchAgents/io.kanolab.farmhand.plist
launchctl load   ~/Library/LaunchAgents/io.kanolab.farmhand.plist
```

---

## 6. Cloudflare Tunnel

```bash
cloudflared tunnel login
cloudflared tunnel create farmhand-macmini    # note the TUNNEL_ID it prints

mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml <<EOF
tunnel: <TUNNEL_ID>
credentials-file: $HOME/.cloudflared/<TUNNEL_ID>.json

ingress:
  - hostname: <your-hostname>      # e.g. macmini.farmhand.example.com
    path: /api/.*
    service: http://127.0.0.1:8080
  - service: http_status:404
EOF

cloudflared tunnel route dns farmhand-macmini <your-hostname>
sudo cloudflared service install
```

The `path: /api/.*` rule keeps the dashboard local-only; only `/api/v1/*` is reachable externally.

---

## 7. Verify end-to-end

```bash
# Public, no auth
curl https://<your-hostname>/api/v1/health

# Auth required
curl -H "Authorization: Bearer $TOKEN" https://<your-hostname>/api/v1/devices
```

Plug an iOS device into the Mac mini, tap **Trust** on the device when prompted, then re-run the `/devices` call — it should show up with `platform: ios` and its UDID.

---

## Updating FarmHand

```bash
cd ~/farmhand-download
gh release download vX.Y.Z --repo caffeaun/farmhand --pattern 'farmhand-darwin-arm64' --clobber
chmod +x farmhand-darwin-arm64
mv farmhand-darwin-arm64 /opt/farmhand/farmhand

launchctl unload ~/Library/LaunchAgents/io.kanolab.farmhand.plist
launchctl load   ~/Library/LaunchAgents/io.kanolab.farmhand.plist
curl http://localhost:8080/api/v1/health
```

---

## Troubleshooting

**`xcrun: error: unable to find utility "xctrace"`** — full Xcode is not installed or `xcode-select` still points at the Command Line Tools. Install Xcode.app from the App Store, then `sudo xcode-select -s /Applications/Xcode.app/Contents/Developer`.

**Device shows up in `xcrun xctrace list devices` but not in FarmHand** — the device must be unlocked and you must have tapped **Trust This Computer** on its prompt. Restart FarmHand after trusting:
```bash
launchctl kickstart -k gui/$(id -u)/io.kanolab.farmhand
```

**launchd service not staying up** — check stderr:
```bash
tail -f /opt/farmhand/logs/stderr.log
```
Common causes: wrong path in the plist, missing `farmhand.yaml`, port 8080 already in use.

**Cloudflare returns 404 for `/api/v1/health`** — the ingress rule order matters. The `path: /api/.*` rule must come before the `http_status:404` fallback.
