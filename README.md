# FarmHand

FarmHand is a self-hosted mobile device farm for running end-to-end tests on real physical Android and iOS devices. It provides a REST API, a web dashboard, and a CLI so teams can schedule test jobs, stream live log output, download artifacts, and monitor device health — all from a single binary.

## Features

- **Device management**: Automatic ADB (Android) and iOS device discovery with live polling. Wireless ADB devices are automatically reconnected when the connection drops, and stable hardware IDs (`ro.serialno` / UDID) keep device records consistent across port changes.
- **Job engine**: Fan-out job scheduling — run a test command on every matching device in parallel.
- **Live log streaming**: Server-Sent Events (SSE) stream of per-device test output.
- **WebSocket live updates**: Real-time device status and job status pushed to the browser.
- **Webhook notifications**: POST job start, completion, and failure events to any URL.
- **Embedded web dashboard**: SvelteKit 2 + Svelte 5 + Tailwind CSS v4 bundled inside the binary.
- **Single binary**: No external runtime dependencies. SQLite for persistence.

## Quick Start

### Prerequisites

- Go 1.24+
- ADB (`adb`) on your `PATH` for Android device support
- (macOS only) `cfgutil` / `ideviceinfo` for iOS device support

### Build

```bash
# Backend only (no embedded UI)
make build

# Backend + embedded UI (requires Node.js and pnpm)
make embed

# Cross-platform release binaries
make release
```

### Run

```bash
# Copy and edit the example config
cp farmhand.example.yaml farmhand.yaml

# Start the server
./bin/farmhand serve

# Or in dev mode (auth disabled, debug logging)
FARMHAND_DEV_MODE=true ./bin/farmhand serve
```

The server listens on `http://0.0.0.0:8080` by default. Open the dashboard at `http://localhost:8080`.

## Configuration

Configuration is loaded from `farmhand.yaml` (or the path given by `--config`). All fields can be overridden with `FARMHAND_*` environment variables.

```yaml
server:
  host: "0.0.0.0"          # FARMHAND_HOST
  port: 8080                # FARMHAND_PORT
  auth_token: ""            # FARMHAND_AUTH_TOKEN — required in production
  cors_origins:
    - "*"
  dev_mode: false           # FARMHAND_DEV_MODE

database:
  path: "farmhand.db"       # FARMHAND_DB_PATH
  retention_days: 30

devices:
  auto_discover: true
  poll_interval_seconds: 5  # FARMHAND_DEVICE_POLL_INTERVAL
  min_battery_percent: 20
  cleanup_between_runs: true
  wake_before_test: true
  adb_path: "adb"           # FARMHAND_ADB_PATH

jobs:
  default_timeout_minutes: 30
  max_concurrent_jobs: 3
  artifact_storage_path: "./artifacts"  # FARMHAND_ARTIFACT_DIR
  result_storage_path: "./results"
  log_dir: "./logs"                     # FARMHAND_LOG_DIR
  max_artifact_size_mb: 500

notifications:
  webhook_url: ""           # FARMHAND_WEBHOOK_URL
  notify_on:
    - failure
    - completion
```

### Auth token

Set `auth_token` to a strong random string. All API requests must include `Authorization: Bearer <token>`. The `GET /api/v1/health` endpoint is public. Leave `auth_token` empty only in `dev_mode`.

## API Reference

All protected endpoints require `Authorization: Bearer <token>`. The base path is `/api/v1`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/health` | Server health, version, and uptime (public) |
| `GET` | `/api/v1/devices` | List devices. Query: `platform`, `tags` (comma-separated) |
| `GET` | `/api/v1/devices/:id` | Get a single device |
| `GET` | `/api/v1/devices/:id/health` | Real-time device health metrics |
| `POST` | `/api/v1/devices/:id/wake` | Send a wake signal |
| `POST` | `/api/v1/devices/:id/reboot` | Initiate a device reboot |
| `POST` | `/api/v1/jobs` | Create and schedule a new job |
| `GET` | `/api/v1/jobs` | List jobs. Query: `status` (queued/running/completed/failed) |
| `GET` | `/api/v1/jobs/:id` | Get a job with per-device results |
| `DELETE` | `/api/v1/jobs/:id` | Cancel a job |
| `GET` | `/api/v1/jobs/:id/status` | Lightweight status poll |
| `GET` | `/api/v1/jobs/:id/logs` | SSE log stream — all devices (`text/event-stream`) |
| `GET` | `/api/v1/jobs/:id/logs/:device_id` | SSE log stream — single device |
| `GET` | `/api/v1/jobs/:id/artifacts` | List job artifacts |
| `GET` | `/api/v1/jobs/:id/artifacts/*filepath` | Download an artifact file |
| `GET` | `/api/v1/config` | Running configuration (auth_token masked) |
| `GET` | `/api/v1/stats` | Device and job counts by status |
| `GET` | `/api/v1/ws` | WebSocket upgrade (`ws://`) |

See [docs/API_REFERENCES.md](docs/API_REFERENCES.md) for the full API reference with request/response shapes.

## CLI Commands

### `farmhand serve`

Starts the FarmHand HTTP server. Reads configuration from `farmhand.yaml` (or `--config`).

```
farmhand serve [--config farmhand.yaml]
```

### `farmhand devices`

Fetches and displays devices from a running FarmHand server.

```
farmhand devices [flags]

Flags:
  --server string   FarmHand server base URL (default "http://localhost:8080")
  --token  string   Bearer auth token (or set FARMHAND_TOKEN env var)
  --format string   Output format: table or json (default "table")
```

### `farmhand run`

Submits a test job to the server and streams log output until completion.

```
farmhand run --command <cmd> [flags]

Flags:
  --command  string         Test command to run on the device (required)
  --install  string         Install command to run before the test command (optional)
  --server   string         FarmHand server base URL (default "http://localhost:8080")
  --token    string         Bearer auth token (or set FARMHAND_TOKEN env var)
  --platform string         Device platform filter: android or ios
  --tags     strings        Device tag filters (comma-separated)
  --timeout  int            Job timeout in minutes (default 30)
  --wait     bool           Stream logs and wait for completion (default true)
```

When `--install` is provided, it runs before `--command` on each device. If the install step fails, the test command is skipped and the device result is marked as failed. This is useful for separating a one-time build step from fan-out test execution.

Exits with code 1 when the job fails or is cancelled.

## Web Dashboard

The web dashboard is served at the root URL when running `farmhand serve`. It is embedded in the binary — no separate deployment is needed.

| Page | URL | Description |
|------|-----|-------------|
| Devices | `/devices` | Device inventory with status, battery, wake/reboot actions |
| Jobs | `/jobs` | Job list with status filter tabs and job creation panel |
| Job detail | `/jobs/:id` | Job results with per-device log switching, live log viewer, artifact downloads |
| Settings | `/settings` | Configure the bearer token for the browser session |

The dashboard connects to the WebSocket endpoint on page load. Device status and job status are updated in real time without polling.

## Architecture

```
farmhand (binary)
├── cmd/farmhand/           CLI entry point (cobra)
│   ├── serve.go            Wires all services, starts HTTP server
│   ├── devices.go          `farmhand devices` command
│   └── run.go              `farmhand run` command
│
├── internal/
│   ├── api/                Gin HTTP handlers and WebSocket hub
│   ├── config/             YAML + env var configuration loading
│   ├── db/                 SQLite repositories (devices, jobs, results)
│   ├── device/             ADB bridge, iOS bridge, device Manager
│   ├── embed/              go:embed wrapper for SvelteKit build output
│   ├── events/             In-process publish/subscribe event bus
│   ├── job/                Scheduler, Runner, Executor, LogCollector, ArtifactCollector
│   ├── log/                zerolog setup, request ID middleware
│   └── notify/             Webhook HTTP notifier
│
└── ui/                     SvelteKit 2 + Svelte 5 + Tailwind CSS v4 dashboard
    └── src/
        ├── lib/api.ts      Typed fetch wrapper for the REST API
        ├── lib/types.ts    Shared TypeScript types
        ├── lib/ws.ts       WebSocket client
        └── routes/         SvelteKit file-based routing
```

### Event bus

The event bus (`internal/events`) is an in-process channel-based pub/sub system. Services publish events (device status changes, job lifecycle transitions), and the WebSocket hub subscribes to broadcast them to connected browsers. Webhook notifications also subscribe to job events.

### WebSocket message types

| Type | Direction | Description |
|------|-----------|-------------|
| `device_snapshot` | server → client | Full device list sent on connect |
| `device_update` | server → client | Single device status change |
| `job_update` | server → client | Job status change |

## Development Setup

### Prerequisites

- Go 1.24+
- Node.js 20+
- pnpm

### Backend

```bash
go mod download
go test -race ./...
go build ./...
```

### Frontend

```bash
cd ui
pnpm install
pnpm check      # svelte-check type checking
pnpm dev        # dev server at http://localhost:5173
pnpm build      # production build to ui/dist/
```

### Full build with embedded UI

```bash
make embed      # builds UI, copies to internal/embed/ui_dist/, compiles Go binary
```

### Running tests

```bash
make test       # go test -race ./...
```

### Linting

```bash
make lint       # golangci-lint run ./...
```

## Deployment

FarmHand runs as a daemon service (systemd on Linux, launchd on macOS) with Cloudflare Tunnel for secure remote API access. The dashboard stays local-only; only `/api/v1/*` endpoints are exposed publicly behind an auth token.

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
                    │ Ubuntu/WSL │  │ Mac Mini    │
                    │ systemd    │  │ launchd     │
                    │ :8080      │  │ :8080       │
                    └─────┬──────┘  └──┬──────────┘
                          │            │
                    ┌─────▼──┐    ┌────▼─────┐
                    │ Android │    │ Android  │
                    │ devices │    │ + iOS    │
                    └────────┘    └──────────┘
```

| Access | What | Auth |
|--------|------|------|
| Local only | Dashboard (`http://localhost:8080`) | None |
| Public (Cloudflare) | API (`https://<host>/api/v1/*`) | Bearer token |

See [docs/use-cases/](docs/use-cases/) for deployment guides:
- [01-ubuntu-with-flutter-android.md](docs/use-cases/01-ubuntu-with-flutter-android.md) — Full Ubuntu setup from scratch: Java 17, Android SDK/NDK, Flutter, Patrol CLI, FarmHand daemon, Cloudflare Tunnel

## License

To be determined.
