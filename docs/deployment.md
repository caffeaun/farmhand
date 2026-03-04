# FarmHand Deployment Guide

This guide covers deploying FarmHand as a daemon service with Cloudflare Tunnel for secure remote API access.

**Key principle**: The web dashboard is only accessible on `localhost`. Only the API (`/api/v1/*`) is exposed through Cloudflare, protected by an auth token.

---

## Prerequisites

- FarmHand binary (built via `make build` or `make release`)
- ADB installed and on `PATH` (for Android devices)
- `cloudflared` CLI ([install guide](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/))
- A Cloudflare account with a domain

---

## 1. Build the binary

On your dev machine:

```bash
# For Linux (amd64)
GOOS=linux GOARCH=amd64 make build

# For macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 make build

# Or build all platforms at once
make release
```

Copy the binary to the target machine:

```bash
scp bin/farmhand-linux-amd64 user@your-host:~/farmhand
```

---

## 2. Install on the target machine

```bash
sudo mkdir -p /opt/farmhand/{artifacts,results,logs}
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand
```

---

## 3. Configure

Create `/opt/farmhand/farmhand.yaml`:

```yaml
server:
  host: "127.0.0.1"          # local only — Cloudflare tunnel handles external access
  port: 8080
  auth_token: "<your-token>"  # required — protects API endpoints
  cors_origins: ["*"]

database:
  path: "/opt/farmhand/farmhand.db"

devices:
  auto_discover: true
  poll_interval_seconds: 5
  adb_path: "/usr/bin/adb"    # adjust for your system

jobs:
  artifact_storage_path: "/opt/farmhand/artifacts"
  result_storage_path: "/opt/farmhand/results"
  log_dir: "/opt/farmhand/logs"
```

### Generate an auth token

```bash
python3 -c "import secrets; print(secrets.token_urlsafe(32))"
```

---

## 4a. Daemon setup — Linux (systemd)

Create `/etc/systemd/system/farmhand.service`:

```ini
[Unit]
Description=FarmHand Device Farm
After=network.target

[Service]
Type=simple
User=farmhand
Group=farmhand
WorkingDirectory=/opt/farmhand
ExecStart=/opt/farmhand/farmhand serve --config /opt/farmhand/farmhand.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo useradd -r -s /usr/sbin/nologin farmhand
sudo chown -R farmhand:farmhand /opt/farmhand
sudo systemctl daemon-reload
sudo systemctl enable --now farmhand
```

Check status:

```bash
sudo systemctl status farmhand
journalctl -u farmhand -f
```

## 4b. Daemon setup — macOS (launchd)

Create `~/Library/LaunchAgents/io.kanolab.farmhand.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.kanolab.farmhand</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/farmhand/farmhand</string>
        <string>serve</string>
        <string>--config</string>
        <string>/opt/farmhand/farmhand.yaml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>WorkingDirectory</key>
    <string>/opt/farmhand</string>
    <key>StandardOutPath</key>
    <string>/opt/farmhand/logs/stdout.log</string>
    <key>StandardErrorPath</key>
    <string>/opt/farmhand/logs/stderr.log</string>
</dict>
</plist>
```

Load the service:

```bash
sudo chown -R $(whoami) /opt/farmhand
launchctl load ~/Library/LaunchAgents/io.kanolab.farmhand.plist
launchctl list | grep farmhand
```

---

## 5. Cloudflare Tunnel

### Install cloudflared

```bash
# Ubuntu/Debian
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o cloudflared.deb
sudo dpkg -i cloudflared.deb

# macOS
brew install cloudflared
```

### Authenticate and create a tunnel

```bash
cloudflared tunnel login
cloudflared tunnel create <tunnel-name>
```

### Configure the tunnel

Create `~/.cloudflared/config.yml`:

```yaml
tunnel: <TUNNEL_ID>
credentials-file: <path-to-credentials-json>

ingress:
  - hostname: <your-hostname>
    path: /api/.*
    service: http://127.0.0.1:8080
  - service: http_status:404
```

The `path: /api/.*` rule ensures **only API endpoints are exposed**. Any request to `/` (the dashboard) returns 404 through the tunnel.

### Route DNS and install as service

```bash
cloudflared tunnel route dns <tunnel-name> <your-hostname>

# Ubuntu — install as systemd service
sudo cloudflared service install

# macOS — install as launchd service
brew services start cloudflared
```

### Verify

```bash
curl https://<your-hostname>/api/v1/health

curl -H "Authorization: Bearer <your-token>" \
  https://<your-hostname>/api/v1/devices
```

---

## Case Study: KanoLab Setup

This is the actual deployment used at KanoLab with two device farm hosts.

### Hosts

| Host | Machine | OS | Daemon | Hostname |
|------|---------|-----|--------|----------|
| devices-1 | PC | Ubuntu (WSL) | systemd | `devices-1.kanolab.io` |
| devices-2 | Mac Mini | macOS | launchd | `devices-2.kanolab.io` |

### Architecture

```
                         ┌─────────────────┐
                         │   Cloudflare     │
                         │   (DNS + Tunnel) │
                         └────┬────────┬────┘
                              │        │
              /api/v1/* only  │        │  /api/v1/* only
              + auth_token    │        │  + auth_token
                              │        │
                    ┌─────────▼──┐  ┌──▼─────────┐
                    │ devices-1  │  │ devices-2   │
                    │ Ubuntu/WSL │  │ Mac Mini    │
                    │ systemd    │  │ launchd     │
                    │ :8080      │  │ :8080       │
                    └─────┬──────┘  └──┬──────────┘
                          │            │
                    ┌─────▼──┐    ┌────▼─────┐
                    │ Android │    │ Android  │
                    │ devices │    │ + iOS    │
                    │ via USB │    │ devices  │
                    └────────┘    └──────────┘
```

### What's exposed vs. local

| Access | Path | Auth |
|--------|------|------|
| Public (via Cloudflare) | `/api/v1/*` | Bearer token required |
| Local only | `/` (dashboard) | No auth needed — `localhost:8080` on the machine |
| Local only | `/api/v1/ws` (WebSocket) | Available on localhost only |

### Setup steps performed

1. Built binaries: `farmhand-linux-amd64` for devices-1, `farmhand-darwin-arm64` for devices-2
2. Installed to `/opt/farmhand/` on both machines
3. Created `farmhand.yaml` with `host: "127.0.0.1"` and a shared auth token
4. Set up systemd service on devices-1 (Ubuntu)
5. Set up launchd plist on devices-2 (Mac Mini)
6. Installed `cloudflared` on both machines
7. Created Cloudflare tunnels: `devices-1` and `devices-2`
8. Configured tunnel ingress with `path: /api/.*` to expose only API routes
9. Routed DNS: `devices-1.kanolab.io` and `devices-2.kanolab.io`
10. Installed `cloudflared` as a system service on both machines

### Testing the deployment

```bash
# Health check (public, no auth)
curl https://devices-1.kanolab.io/api/v1/health

# List devices (requires auth)
curl -H "Authorization: Bearer $FARMHAND_TOKEN" \
  https://devices-1.kanolab.io/api/v1/devices

# Submit a job
curl -X POST https://devices-1.kanolab.io/api/v1/jobs \
  -H "Authorization: Bearer $FARMHAND_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "test_command": "python3 /opt/scripts/flow-runner.py /opt/flows/my-test.yaml",
    "device_filter": {"platform": "android"},
    "timeout_minutes": 30
  }'

# Dashboard — local access only
# SSH into the machine, then open http://localhost:8080
```

---

## Updating FarmHand

```bash
# 1. Build a new binary
make build

# 2. Copy to the target machine
scp bin/farmhand user@host:~/farmhand

# 3. On the target machine
sudo systemctl stop farmhand            # or: launchctl unload ~/Library/LaunchAgents/io.kanolab.farmhand.plist
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand
sudo systemctl start farmhand           # or: launchctl load ~/Library/LaunchAgents/io.kanolab.farmhand.plist
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `farmhand` won't start | Check `journalctl -u farmhand -f` (Linux) or `/opt/farmhand/logs/stderr.log` (macOS) |
| Tunnel not working | Run `cloudflared tunnel info <name>` and check `systemctl status cloudflared` |
| Dashboard accessible via tunnel | Verify `path: /api/.*` is in your cloudflared `config.yml` |
| ADB devices not found | Ensure the `farmhand` user has access to USB devices (`adb devices` as that user) |
| Auth token rejected | Compare token in `farmhand.yaml` with what you're sending in the `Authorization` header |
