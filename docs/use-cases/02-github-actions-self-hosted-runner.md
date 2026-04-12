# Use Case 02: GitHub Actions Self-Hosted Runner on Ubuntu

Offload CI/CD minutes from GitHub-hosted runners to your own Ubuntu machine. This is useful when you've hit the GitHub Actions free-tier limit and want to run workflows on hardware you already own.

## Why Self-Hosted?

| | GitHub-hosted | Self-hosted |
|--|---------------|-------------|
| Cost | 2,000 free mins/month, then $0.008/min | Free (your hardware) |
| Hardware | Shared VM, no USB access | Full control, USB devices, GPUs |
| Speed | Cold start, download deps every run | Warm cache, pre-installed tools |
| Privacy | Code runs on GitHub's infra | Code never leaves your machine |

## Prerequisites

- Ubuntu machine (physical or WSL)
- GitHub repository (or organization) with admin access
- `sudo` access on the Ubuntu machine

## Step 1: Add a Self-Hosted Runner in GitHub

1. Go to your GitHub repository (or organization)
2. Navigate to **Settings → Actions → Runners**
3. Click **New self-hosted runner**
4. Select **Linux** and **x64**
5. Copy the **token** shown on the page — you'll need it in Step 3

> For organization-level runners, you need a Personal Access Token with `admin:org` scope instead.

## Step 2: Install the Runner

```bash
# Create a dedicated directory
mkdir -p ~/actions-runner && cd ~/actions-runner

# Download the latest runner package (check GitHub for the current version)
curl -o actions-runner-linux-x64-2.322.0.tar.gz -L \
  https://github.com/actions/runner/releases/download/v2.322.0/actions-runner-linux-x64-2.322.0.tar.gz

# Extract
tar xzf ./actions-runner-linux-x64-2.322.0.tar.gz
```

## Step 3: Configure the Runner

```bash
# Replace <OWNER>/<REPO> and <TOKEN> with your values from Step 1
./config.sh --url https://github.com/<OWNER>/<REPO> --token <TOKEN>
```

You will be prompted for:

| Prompt | Recommended value |
|--------|-------------------|
| Runner group | `Default` (press Enter) |
| Runner name | A name for this machine, e.g. `ubuntu-builder` |
| Labels | `self-hosted,linux,x64` (add custom labels as needed) |
| Work folder | `_work` (press Enter) |

> **Tip**: Custom labels (e.g. `gpu`, `android`, `flutter`) let you target specific runners in workflows with `runs-on: [self-hosted, flutter]`.

## Step 4: Install as a systemd Service

```bash
# Install the service (requires sudo)
sudo ./svc.sh install

# Start the service
sudo ./svc.sh start

# Verify it's running
sudo ./svc.sh status
```

The runner starts automatically on boot.

## Step 5: Configure Environment for the Runner Service

The systemd service runs in a clean environment. If your workflows need tools like Java, Android SDK, Flutter, etc., add them to the service environment:

```bash
# Find the service name
systemctl list-units | grep actions.runner

# Edit the systemd override
sudo systemctl edit actions.runner.<OWNER>-<REPO>.<RUNNER_NAME>.service
```

Add the paths your workflows need:

```ini
[Service]
Environment="JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64"
Environment="ANDROID_HOME=/home/<user>/Android/Sdk"
Environment="PATH=/home/<user>/flutter/bin:/home/<user>/Android/Sdk/platform-tools:/usr/local/bin:/usr/bin:/bin"
```

Then reload:

```bash
sudo systemctl daemon-reload
sudo systemctl restart actions.runner.<OWNER>-<REPO>.<RUNNER_NAME>.service
```

## Step 6: Update Your Workflow to Use the Self-Hosted Runner

Change `runs-on` from `ubuntu-latest` to `self-hosted` in your workflow files:

```yaml
# Before (uses GitHub-hosted runner, consumes minutes)
jobs:
  build:
    runs-on: ubuntu-latest

# After (uses your self-hosted runner, free)
jobs:
  build:
    runs-on: self-hosted
```

If you added custom labels, you can target specific runners:

```yaml
jobs:
  build:
    runs-on: [self-hosted, flutter]
```

Everything else in the workflow stays the same — `actions/checkout`, `run` steps, secrets, artifacts all work identically.

### Example: Build Once, Test on Many Devices (with FarmHand)

When using FarmHand for device testing, separate the build step from the test step. The build runs once on the runner, then FarmHand fans out the lightweight install + test to all devices in parallel:

```yaml
name: Device Farm Tests

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  workflow_dispatch:

jobs:
  device-test:
    runs-on: [self-hosted, farmhand]
    timeout-minutes: 30

    steps:
      - uses: actions/checkout@v4

      - name: Build APKs (runs once)
        run: |
          flutter build apk --debug
          flutter build apk --debug --target integration_test/app_test.dart

      - name: Run tests on all devices (fan-out)
        run: |
          farmhand run \
            --server http://localhost:8080 \
            --token "${{ secrets.FARMHAND_TOKEN }}" \
            --install "adb -s \$FARMHAND_DEVICE_SERIAL install -r build/app/outputs/flutter-apk/app-debug.apk && adb -s \$FARMHAND_DEVICE_SERIAL install -r build/app/outputs/flutter-apk/app-debug-androidTest.apk" \
            --command "adb -s \$FARMHAND_DEVICE_SERIAL shell am instrument -w -e class com.example.app.MainActivityTest com.example.app.test/androidx.test.runner.AndroidJUnitRunner" \
            --platform android \
            --timeout 30
```

The `--install` flag runs before `--command` on each device. If install fails on a device, the test is skipped for that device and the result is marked as failed.

## Step 7: Verify the Setup

1. **Check runner status** on GitHub:
   - Go to **Settings → Actions → Runners**
   - Your runner should show as **Idle** with a green dot

2. **Trigger a test run**:
   - Push a commit or go to **Actions → select workflow → Run workflow**

3. **Check logs**:
   ```bash
   journalctl -u actions.runner.<OWNER>-<REPO>.<RUNNER_NAME>.service -f
   ```

## Managing the Runner

```bash
# Stop the runner
sudo ./svc.sh stop

# Restart
sudo ./svc.sh start

# Uninstall the service
sudo ./svc.sh uninstall

# Reconfigure (e.g. change labels)
./config.sh remove --token <TOKEN>
./config.sh --url https://github.com/<OWNER>/<REPO> --token <NEW_TOKEN>
sudo ./svc.sh install && sudo ./svc.sh start
```

## Security Considerations

- **Do not use self-hosted runners on public repos.** Anyone who can open a PR can run arbitrary code on your machine.
- Keep the runner updated: `cd ~/actions-runner && ./bin/Runner.Listener --check`
- The runner auto-updates by default when GitHub releases a new version.
- Consider running the service as a dedicated user with limited permissions.

## Troubleshooting

### Runner shows as offline

```bash
# Check service status
sudo ./svc.sh status

# Check logs for errors
journalctl -u actions.runner.<OWNER>-<REPO>.<RUNNER_NAME>.service --no-pager -n 50
```

### Workflow stuck on "Waiting for a runner"

- Verify `runs-on` labels match the runner's labels
- Check that the runner is online in **Settings → Actions → Runners**
- If using org-level runner, ensure the repo has access in runner group settings

### Tools not found during workflow

The systemd service has a minimal environment. Either:
1. Add paths via `sudo systemctl edit` (see Step 5)
2. Or source your profile in the workflow step:
   ```yaml
   - run: |
       source ~/.bashrc
       flutter build apk
   ```

## Architecture

```
GitHub.com
    │
    │  webhook (push / PR / dispatch)
    │
    ▼
┌─────────────────────────┐
│  Ubuntu Machine         │
│                         │
│  ┌───────────────────┐  │
│  │ GitHub Actions     │  │
│  │ Runner (systemd)   │  │
│  │                   │  │
│  │ runs workflow     │  │
│  │ steps locally     │  │
│  └───────────────────┘  │
│                         │
│  Pre-installed tools:   │
│  - Flutter, Java, ADB  │
│  - Docker, Node, etc.  │
└─────────────────────────┘
```

All workflow steps execute directly on your machine. Build caches persist between runs, dependencies stay installed, and you pay $0 in CI minutes.
