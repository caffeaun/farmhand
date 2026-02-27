# Changelog

All notable changes to FarmHand are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-02-27

### Added

#### Go Backend

- **Configuration** (`internal/config`): YAML-based configuration loading with sane defaults and `FARMHAND_*` environment variable overrides. Supported env vars include `FARMHAND_HOST`, `FARMHAND_PORT`, `FARMHAND_AUTH_TOKEN`, `FARMHAND_DEV_MODE`, `FARMHAND_DB_PATH`, `FARMHAND_LOG_DIR`, `FARMHAND_ARTIFACT_DIR`, `FARMHAND_DEVICE_POLL_INTERVAL`, `FARMHAND_WEBHOOK_URL`, and `FARMHAND_ADB_PATH`.

- **SQLite persistence** (`internal/db`): Embedded SQLite database via `modernc.org/sqlite` with auto-migration on startup. Repositories for devices, jobs, and job results. Schema covers device inventory, job records with status lifecycle, and per-device execution results.

- **Device management** (`internal/device`): ADB bridge for Android device discovery and management. iOS bridge for macOS (`cfgutil`/`idevice*`). A polling `Manager` that discovers connected devices, updates their status (online/offline/busy), and emits device events on the bus. Configurable poll interval and minimum battery threshold.

- **Job engine** (`internal/job`): `Scheduler` that selects available devices matching the job's device filter and strategy. `Runner` that executes the test command on each device via `/bin/sh -c`, streams output to per-job log files, collects artifacts, reports results to the database, and publishes job events. `LogCollector` for tailing log files via SSE. `ArtifactCollector` for listing and reading job artifact files by MIME type.

- **Event bus** (`internal/events`): In-process channel-based publish/subscribe bus. Events: `DeviceOnline`, `DeviceOffline`, `DeviceStatusChanged`, `JobStarted`, `JobCompleted`, `JobFailed`.

- **Webhook notifications** (`internal/notify`): HTTP POST webhook on job start, completion, and failure. Configurable via `notifications.webhook_url` and `notifications.notify_on`.

#### REST API (`internal/api`)

- **Auth middleware**: Static bearer token validation using `Authorization: Bearer <token>` header or `?token=` query parameter (for WebSocket upgrades). Constant-time comparison to prevent timing attacks. When `auth_token` is empty, auth is disabled (dev mode).

- **CORS middleware**: Configurable allowed origins, handles OPTIONS preflight with HTTP 204.

- **Health endpoint** `GET /api/v1/health` (public): Returns server status, version, and uptime.

- **Device endpoints**: `GET /api/v1/devices`, `GET /api/v1/devices/:id`, `GET /api/v1/devices/:id/health`, `POST /api/v1/devices/:id/wake`, `POST /api/v1/devices/:id/reboot`. Platform and tag query filtering on list.

- **Job endpoints**: `POST /api/v1/jobs`, `GET /api/v1/jobs`, `GET /api/v1/jobs/:id`, `DELETE /api/v1/jobs/:id`. Job creation triggers scheduling and async execution in a goroutine. List supports `?status=` filter.

- **Log streaming** `GET /api/v1/jobs/:id/logs`: Server-Sent Events stream of live job output. Sends `event: done` when streaming ends. Context-cancelled on client disconnect; no goroutine leak.

- **Job status** `GET /api/v1/jobs/:id/status`: Lightweight status poll endpoint returning id, status, created_at, started_at, completed_at.

- **Artifact endpoints**: `GET /api/v1/jobs/:id/artifacts` lists all artifact files across devices. `GET /api/v1/jobs/:id/artifacts/*filepath` streams artifact file bytes with path traversal protection.

- **System endpoints**: `GET /api/v1/config` returns the running configuration with `auth_token` masked. `GET /api/v1/stats` returns real-time device and job counts by status.

- **WebSocket** `GET /api/v1/ws`: Upgrades to WebSocket. On connect, sends a `device_snapshot` with all current devices. Broadcasts `device_update` and `job_update` messages as events arrive from the bus. Maximum 100 concurrent connections.

#### Web Dashboard (SvelteKit 2 + Svelte 5 + Tailwind CSS v4)

- **Devices page** (`/devices`): Table view of all connected devices showing model, platform, OS version, status badge, battery level (with low-battery warning), last-seen timestamp. Wake and Reboot action buttons with in-flight state. WebSocket live updates patch device rows in-place. Toast notifications for action outcomes.

- **Jobs page** (`/jobs`): Filterable jobs table (All / Queued / Running / Completed / Failed tabs driven by URL `?status=` param). Slide-over panel for creating new jobs with test command, platform filter, tag filter, and timeout. Delete confirmation dialog. WebSocket live status updates.

- **Job detail page** (`/jobs/:id`): Full job record, per-device results, live SSE log viewer with auto-scroll and scroll-lock, artifact list with download links.

- **Settings page** (`/settings`): Bearer token configuration stored in `localStorage`.

- **Navigation**: Persistent sidebar with links to Devices, Jobs, and Settings. Root path redirects to `/devices`.

- **go:embed UI assets**: SvelteKit build output is embedded into the Go binary via `go:embed`. The `serve` command serves the SPA as a NoRoute fallback so all non-API paths load `index.html` for client-side routing.

#### CLI (`cmd/farmhand`)

- `farmhand serve`: Starts the HTTP server. Loads configuration, initialises all services (database, device manager, job runner, WebSocket hub), and starts a graceful shutdown listener for `SIGINT`/`SIGTERM`.

- `farmhand devices [--server] [--token] [--format]`: Fetches and prints the device list from a running server as a formatted table or JSON.

- `farmhand run --command <cmd> [--server] [--token] [--platform] [--tags] [--timeout] [--wait]`: Submits a test job and, by default, streams live log output via SSE until the job completes. Exits with code 1 when the job fails.

- Global `--config` flag for specifying the YAML config file path (default: `farmhand.yaml`).
- `--version` flag prints the build-time version tag.

#### Build

- `Makefile` targets: `build`, `ui-build`, `embed-copy`, `embed`, `release`, `test`, `lint`, `clean`, `run`.
- Cross-platform `make release` targets: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
- Version injected at build time via `-ldflags "-X main.version=<tag>"`.

[0.1.0]: https://github.com/caffeaun/farmhand/releases/tag/v0.1.0
