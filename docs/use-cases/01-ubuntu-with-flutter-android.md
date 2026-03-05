# Ubuntu + Flutter + Android Devices

Full setup guide: Ubuntu (WSL) host running FarmHand as a systemd daemon with Flutter/Patrol for Android integration testing, exposed via Cloudflare Tunnel.

**Key principle**: The web dashboard is only accessible on `localhost`. Only the API (`/api/v1/*`) is exposed through Cloudflare, protected by an auth token.

**Related docs**: [API Reference](../API_REFERENCES.md) | [README](../../README.md)

---

## Table of Contents

- [Quick Reference](#quick-reference)
- [Generic Deployment](#generic-deployment)
  - [1. Build the binary](#1-build-the-binary)
  - [2. Install on the target machine](#2-install-on-the-target-machine)
  - [3. Configure FarmHand](#3-configure-farmhand)
  - [4a. Daemon — Linux (systemd)](#4a-daemon--linux-systemd)
  - [4b. Daemon — macOS (launchd)](#4b-daemon--macos-launchd)
  - [5. Cloudflare Tunnel](#5-cloudflare-tunnel)
- [Case Study: KanoLab Ubuntu Setup](#case-study-kanolab-ubuntu-setup)
  - [Full stack install on Ubuntu](#full-stack-install-on-ubuntu)
  - [KanoLab architecture](#kanolab-architecture)
- [Updating FarmHand](#updating-farmhand)
- [Troubleshooting](#troubleshooting)

---

## Quick Reference

| Component | Purpose |
|-----------|---------|
| FarmHand binary | Device farm server (`/opt/farmhand/farmhand`) |
| `farmhand.yaml` | Server config — auth token, DB path, device settings |
| systemd / launchd | Keeps FarmHand running as a daemon |
| Cloudflare Tunnel | Exposes only `/api/v1/*` to the internet (no open ports) |
| Java 17 + Android SDK | Required for building and running Android tests |
| Flutter SDK + Patrol CLI | Required if running Flutter integration tests |

---

## Generic Deployment

### 1. Build the binary

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

### 2. Install on the target machine

```bash
sudo mkdir -p /opt/farmhand/{artifacts,results,logs}
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand
```

### 3. Configure FarmHand

Generate an auth token:

```bash
python3 -c "import secrets; print(secrets.token_urlsafe(32))"
```

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

See [farmhand.example.yaml](../farmhand.example.yaml) for all available options with descriptions.

### 4a. Daemon — Linux (systemd)

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

### 4b. Daemon — macOS (launchd)

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

### 5. Cloudflare Tunnel

#### Install cloudflared

```bash
# Ubuntu/Debian
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o cloudflared.deb
sudo dpkg -i cloudflared.deb

# macOS
brew install cloudflared
```

#### Authenticate and create a tunnel

```bash
cloudflared tunnel login
cloudflared tunnel create <tunnel-name>
```

#### Configure the tunnel

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

#### Route DNS and install as service

```bash
cloudflared tunnel route dns <tunnel-name> <your-hostname>

# Ubuntu — install as systemd service
sudo cloudflared service install

# macOS — install as launchd service
brew services start cloudflared
```

#### Verify

```bash
# Health check (public, no auth needed)
curl https://<your-hostname>/api/v1/health

# List devices (requires auth)
curl -H "Authorization: Bearer <your-token>" \
  https://<your-hostname>/api/v1/devices
```

See [API Reference](api.md) for all available endpoints.

---

## Case Study: KanoLab Ubuntu Setup

This is the actual deployment used at KanoLab — a complete walkthrough of setting up an Ubuntu (WSL) machine from scratch as a FarmHand host for Android device testing with Flutter/Patrol.

### Full stack install on Ubuntu

#### Step 1: Java 17

Required by Android SDK build tools and Gradle.

```bash
sudo apt update
sudo apt install -y openjdk-17-jdk

# Verify
java -version   # openjdk 17.x.x

# Set JAVA_HOME (add to ~/.bashrc or ~/.profile)
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
```

#### Step 2: Android SDK + NDK

```bash
# Install Android command-line tools
sudo apt install -y unzip wget
mkdir -p ~/android-sdk/cmdline-tools
cd ~/android-sdk/cmdline-tools
wget https://dl.google.com/android/repository/commandlinetools-linux-11076708_latest.zip -O tools.zip
unzip tools.zip
mv cmdline-tools latest
rm tools.zip

# Set environment variables (add to ~/.bashrc)
export ANDROID_HOME=$HOME/android-sdk
export ANDROID_SDK_ROOT=$ANDROID_HOME
export PATH=$PATH:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools

# Accept licenses and install components
sdkmanager --licenses
sdkmanager "platform-tools" "platforms;android-34" "build-tools;34.0.0" "ndk;26.1.10909125"

# Verify ADB
adb version
```

#### Step 3: Flutter SDK

```bash
# Install Flutter via snap (recommended for Ubuntu)
sudo snap install flutter --classic

# Or manual install
cd ~
git clone https://github.com/flutter/flutter.git -b stable
export PATH=$PATH:$HOME/flutter/bin

# Add to ~/.bashrc
export PATH=$PATH:$HOME/flutter/bin       # if manual install
# or snap handles PATH automatically

# Verify and accept licenses
flutter doctor
flutter doctor --android-licenses
```

#### Step 4: Patrol CLI

[Patrol](https://patrol.leancode.co/) is a Flutter integration testing framework.

```bash
# Install Patrol CLI
dart pub global activate patrol_cli

# Add dart pub global bin to PATH (add to ~/.bashrc)
export PATH=$PATH:$HOME/.pub-cache/bin

# Verify
patrol --version
```

#### Step 5: FarmHand

Build on your dev machine and copy to the host:

```bash
# On dev machine
GOOS=linux GOARCH=amd64 make build
scp bin/farmhand user@devices-1.kanolab.io:~/farmhand
```

On the Ubuntu host:

```bash
# Install
sudo mkdir -p /opt/farmhand/{artifacts,results,logs}
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand

# Generate auth token
python3 -c "import secrets; print(secrets.token_urlsafe(32))"

# Create config
sudo tee /opt/farmhand/farmhand.yaml > /dev/null << 'EOF'
server:
  host: "127.0.0.1"
  port: 8080
  auth_token: "<paste-token-here>"
  cors_origins: ["*"]

database:
  path: "/opt/farmhand/farmhand.db"

devices:
  auto_discover: true
  poll_interval_seconds: 5
  adb_path: "/usr/bin/adb"

jobs:
  artifact_storage_path: "/opt/farmhand/artifacts"
  result_storage_path: "/opt/farmhand/results"
  log_dir: "/opt/farmhand/logs"
EOF

# Create systemd service
sudo tee /etc/systemd/system/farmhand.service > /dev/null << 'EOF'
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
EOF

# Create user and start
sudo useradd -r -s /usr/sbin/nologin farmhand
sudo chown -R farmhand:farmhand /opt/farmhand
sudo systemctl daemon-reload
sudo systemctl enable --now farmhand
sudo systemctl status farmhand
```

#### Step 6: Cloudflare Tunnel

```bash
# Install cloudflared
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o cloudflared.deb
sudo dpkg -i cloudflared.deb

# Login and create tunnel
cloudflared tunnel login
cloudflared tunnel create devices-1

# Configure (replace TUNNEL_ID and credentials path)
mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml << 'EOF'
tunnel: <TUNNEL_ID>
credentials-file: /home/<user>/.cloudflared/<TUNNEL_ID>.json

ingress:
  - hostname: devices-1.kanolab.io
    path: /api/.*
    service: http://127.0.0.1:8080
  - service: http_status:404
EOF

# Route DNS and install as system service
cloudflared tunnel route dns devices-1 devices-1.kanolab.io
sudo cloudflared service install
```

#### Step 7: Connect Android devices and verify

```bash
# Plug in Android devices via USB, then:
adb devices

# Verify FarmHand sees them
curl http://localhost:8080/api/v1/devices

# Verify remote API access
curl https://devices-1.kanolab.io/api/v1/health
curl -H "Authorization: Bearer <your-token>" \
  https://devices-1.kanolab.io/api/v1/devices
```

#### Step 8: Submit a test job

```bash
# Run a Patrol test via FarmHand
curl -X POST https://devices-1.kanolab.io/api/v1/jobs \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "test_command": "cd /path/to/flutter/project && patrol test --device $FARMHAND_DEVICE_SERIAL",
    "device_filter": {"platform": "android"},
    "timeout_minutes": 30
  }'
```

#### Environment summary

After completing all steps, add to `~/.bashrc`:

```bash
# Java
export JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

# Android SDK
export ANDROID_HOME=$HOME/android-sdk
export ANDROID_SDK_ROOT=$ANDROID_HOME
export PATH=$PATH:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools

# Flutter
export PATH=$PATH:$HOME/flutter/bin  # if manual install

# Dart pub global (Patrol CLI)
export PATH=$PATH:$HOME/.pub-cache/bin
```

### KanoLab architecture

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

| Host | Machine | OS | Daemon | Hostname |
|------|---------|-----|--------|----------|
| devices-1 | PC | Ubuntu (WSL) | systemd | `devices-1.kanolab.io` |
| devices-2 | Mac Mini | macOS | launchd | `devices-2.kanolab.io` |

### What's exposed vs. local

| Access | Path | Auth |
|--------|------|------|
| Public (via Cloudflare) | `/api/v1/*` | Bearer token required |
| Local only | `/` (dashboard) | No auth needed — `localhost:8080` on the machine |
| Local only | `/api/v1/ws` (WebSocket) | Available on localhost only |

### Installed stack (devices-1, Ubuntu)

| Component | Version | Purpose |
|-----------|---------|---------|
| Java (OpenJDK) | 17 | Android build tools, Gradle |
| Android SDK | 34 | Platform tools, ADB |
| Android NDK | 26.1 | Native builds (Flutter) |
| Flutter SDK | stable | Build and run Flutter tests |
| Patrol CLI | latest | Flutter integration test framework |
| FarmHand | latest | Device farm server |
| cloudflared | latest | Cloudflare Tunnel daemon |

---

## Updating FarmHand

```bash
# 1. Build a new binary on your dev machine
make build

# 2. Copy to the target machine
scp bin/farmhand user@host:~/farmhand

# 3. On the target machine — restart the service
sudo systemctl stop farmhand            # Linux
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand
sudo systemctl start farmhand           # Linux

# macOS equivalent:
launchctl unload ~/Library/LaunchAgents/io.kanolab.farmhand.plist
sudo mv ~/farmhand /opt/farmhand/farmhand
sudo chmod +x /opt/farmhand/farmhand
launchctl load ~/Library/LaunchAgents/io.kanolab.farmhand.plist
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| FarmHand won't start | Check `journalctl -u farmhand -f` (Linux) or `/opt/farmhand/logs/stderr.log` (macOS) |
| Tunnel not working | Run `cloudflared tunnel info <name>` and `systemctl status cloudflared` |
| Dashboard accessible via tunnel | Verify `path: /api/.*` is in your cloudflared `config.yml` |
| ADB devices not found | Ensure the `farmhand` user has access to USB devices — run `adb devices` as that user |
| Auth token rejected | Compare token in `farmhand.yaml` with what you're sending in the `Authorization` header |
| `flutter doctor` shows issues | Run `flutter doctor --android-licenses` and ensure `JAVA_HOME` is set |
| Patrol not found | Ensure `$HOME/.pub-cache/bin` is in `PATH` |
| Android SDK not found | Ensure `ANDROID_HOME` and `ANDROID_SDK_ROOT` are set in `~/.bashrc` |
