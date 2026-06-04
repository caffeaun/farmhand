# Changelog

All notable changes to FarmHand are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.6.1] - 2026-06-04

### Added

- `farmhand clear --device <id>` — kills every background app on the device (`am kill-all`) and then sends `KEYCODE_HOME` so the launcher is foregrounded. Useful as a clean-slate step between tests.
- `farmhand launch --device <id> --package <pkg>` — starts the main launcher activity of an installed Android package via `am start --pn <pkg>`. The package id is validated against `^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$` at the bridge before reaching the device shell, so it cannot smuggle extra adb arguments.
- `ADBBridge.KillAllApps(serial)` and `ADBBridge.Launch(serial, pkg)` methods on `internal/device/android.go`; `packageIDPattern` unexported regex for the launch path.
- `Manager.KillAllApps(id)` and `Manager.Launch(id, pkg)` with the standard five-guard pattern (`FindByID` → `ErrNotFound`; offline → 409-shape; non-Android → unsupported-platform; nil-adb → not-configured; else bridge call). Both methods added to the consumer-side `adbDriver` interface.

### Changed

- `cmd/farmhand` shared `deviceManagerCLI` interface extended with `KillAllApps` and `Launch`; the production `Manager` already satisfies it.

### Notes

- `am start --pn` requires Android 10 (API 29) or later. On older devices `farmhand launch` will need the activity-class form; deferred until a real device demands it.
- `kanoonthteam/tap`'s `clear.sh` and `launch.sh` (appId branch) should migrate from direct `adb shell` to these subcommands once devices-1 deploys the v0.6.1 binary.

---

## [0.6.0] - 2026-06-02

### Added

#### Device-input CLI (Phase A)

- `farmhand tap --device X --x N --y N` — single tap at pixel coordinates via the ADB bridge (no adb shell-out from job scripts).
- `farmhand swipe --device X --from-x ... --to-x ... [--duration-ms]` — swipe gesture; deadline scales with the gesture duration so long swipes don't timeout.
- `farmhand keyevent --device X --keycode <KEYCODE_X | int>` — keyevent dispatch with keycode validated against `^KEYCODE_[A-Z0-9_]+$` or non-negative integer; rejects arbitrary strings before they reach the device shell.
- `farmhand text --device X --text "..."` — types text into the focused field; the bridge single-quote-escapes the text on the device side so embedded shell metacharacters (`;`, `&`, `|`, `$`, backticks) stay literal.
- `ADBBridge.Tap/Swipe/KeyEvent/InputText` methods + `quoteForDeviceShell` helper in `internal/device/android.go`.
- `Manager.Tap/Swipe/KeyEvent/InputText` with the standard five-guard pattern (FindByID → ErrNotFound; offline → 409-shape; non-Android → unsupported-platform; nil ADB → not-configured; else bridge call).
- `docs/cli.md` and `docs/use-cases/04-android-tap-stress.md` (stress job whose `test_command` uses `farmhand tap` instead of `adb`).

#### Device capture as bridge methods (Phase B)

- `ADBBridge.Screenshot(serial)` — returns raw PNG bytes from `adb -s <serial> exec-out screencap -p`. Implemented via new binary-safe helpers `runRaw` / `runDeviceRaw`.
- `ADBBridge.Logcat(serial, opts)` — returns the device's logcat buffer; `LogcatOptions{Since, Filter}` lets callers bound the dump. Filter values restricted to `V/D/I/W/E/F/S` allow-list.
- `Manager.Screenshot` and `Manager.Logcat` mirror the same five guards as the input methods.
- No REST endpoint and no CLI subcommand — these are Go methods only; future work (executor auto-capture into the artifact dir) consumes them in-process.

#### Vision-driven inspect (Phase C)

- `farmhand inspect --device X` — takes a real screenshot via the bridge, POSTs it to the configured vision LLM (MiniMax-M3 by default) with a forced tool-call (`report_inspection`), and prints the topic list as JSON: `{topics: [{name, coordinates: {x1, y1, x2, y2}, color, type, text}], screenshot_size}`. Bounding-box centers are intended to be tapped via `farmhand tap`.
- `--mock-from <file>` flag — skips the vision provider entirely and reads the topic list from a local JSON file. Real screenshot + real PNG-header decode + mocked topics; lets you E2E the inspect → match → tap chain on real hardware without spending LLM tokens or provisioning an API key.
- `internal/vision` package: `Client` interface (`Inspect(ctx, png)`), `Topic` / `Box` / `InspectResult` types, and `MiniMaxClient` over stdlib `net/http`. OpenAI-compatible request shape with image_url base64 data URI and forced tool_choice. No external SDK; httptest-driven tests cover happy path, empty topics, non-2xx, malformed JSON, missing tool call, malformed tool-call arguments, and context cancellation.
- `vision:` block in config (`provider`, `api_key_env`, `base_url`, `model`, `timeout_sec`, `detail`); commented example in `farmhand.example.yaml`. API key resolved lazily from the env variable named by `api_key_env` (default `MINIMAX_API_KEY`); empty key disables the command with a clear error.
- `docs/use-cases/05-vision-driven-tap.md` — inspect-and-tap script with name/text/color/type filtering examples plus the `--mock-from` workflow for E2E testing without an LLM.

### Changed

- `adbDriver` interface (consumer-side, in `internal/device/manager.go`) extended with the six new methods.
- Default config defaults gain a `Vision` block (MiniMax-M3, `https://api.minimax.io/v1`, 15s timeout, `detail: high`).

### Notes

- All CLI subcommands talk to the SQLite DB directly (the same file `farmhand serve` polls into) — no HTTP round-trip. The runner host must have `farmhand serve` running (or have run recently) so the `devices` table is populated for `FindByID` lookups.
- Per the architecture rule, no job runner ever shells out to `adb` — `kanoonthteam/tap`'s `tap.sh` should be rewritten to call `farmhand tap` in a follow-up.

---

## [0.5.0] - 2026-05-30

### Added

- `internal/job/CancelRegistry` — thread-safe, lock-protected map from job ID to `context.CancelFunc`. Supports `Register`, `Cancel` (cancel-then-remove in one lock), `Remove`, and `Has` operations. Includes a `NewCancelRegistry` constructor wired into `cmd/farmhand/serve.go`.
- `events.JobCancelled` constant (`"job.cancelled"`) in `internal/events/events.go`.
- `notify.EventJobCancelled` constant (`"job.cancelled"`) in `internal/notify/notify.go`.

### Changed

- `DELETE /api/v1/jobs/:id` now performs **real cancellation** — the runner's context is cancelled, which causes the executor to send SIGKILL to the shell process group, freeing affected devices back to `online`. Per-device `JobResult` rows are written with `status="cancelled"`, `exit_code=-1`, and `error_message="cancelled by user"`.
- After cancellation the runner publishes `events.JobCancelled` (`"job.cancelled"`) to the internal event bus and to the webhook notifier (`notify.EventJobCancelled`). The webhook fires when `"cancelled"` is listed under `notify_on` in the server configuration.
- The endpoint is **idempotent**: calling `DELETE` on a job that is already in a terminal state (`completed`, `failed`, or `cancelled`) still returns `204 No Content` with no side effects.
- Cancellation is **in-memory only** — the cancel registry does not persist across server restarts. In-flight jobs at shutdown are handled by startup recovery (`RunRecovery`), which marks any `running`/`preparing`/`installing` jobs as `failed` on next server start.

---

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
