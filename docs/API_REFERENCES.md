# FarmHand API Reference

**Related docs**: [Use Cases](use-cases/) | [README](../README.md)

Base path: `/api/v1`

## Authentication

All endpoints except `GET /api/v1/health` require a bearer token.

**Header (preferred)**
```
Authorization: Bearer <token>
```

**Query parameter (WebSocket upgrades)**
```
GET /api/v1/ws?token=<token>
```

When `auth_token` is empty in the server configuration, authentication is disabled and all requests are accepted.

Incorrect or missing tokens return:
```
HTTP 401 Unauthorized
{"error": "unauthorized"}
```

---

## Health

### GET /api/v1/health

Public endpoint. Returns server status, version, and uptime.

**Response — 200 OK**
```json
{
  "status": "ok",
  "version": "v0.1.0",
  "uptime_seconds": 3600
}
```

---

## Devices

### GET /api/v1/devices

List all registered devices, optionally filtered.

**Query parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `platform` | string | Filter by platform: `android` or `ios` |
| `tags` | string | Comma-separated list of tags. Device must have ALL specified tags. |

**Response — 200 OK**

Returns an array (empty array when no results, never null).

```json
[
  {
    "id": "192.168.20.117:42893",
    "serial": "192.168.20.117:42893",
    "model": "SM-A166P",
    "platform": "android",
    "os_version": "14",
    "status": "online",
    "battery_level": 87,
    "hardware_id": "R58N12345AB",
    "tags": ["production", "ci"],
    "last_seen_at": "2026-02-27T10:00:00Z"
  }
]
```

**Device status values**: `online`, `offline`, `busy`

**`hardware_id`**: Stable device identifier that persists across serial/port changes. For Android devices this is `ro.serialno`; for iOS devices it is the UDID. When a wireless device reconnects with a new port, FarmHand uses `hardware_id` to recognize the same physical device and merges the records (preserving tags).

**Wireless auto-reconnect**: Wireless ADB devices (identified by `IP:port` serial format) that go offline are automatically reconnected via `adb connect` on each poll cycle. No configuration needed.

---

### GET /api/v1/devices/:id

Get a single device by ID.

**Response — 200 OK**

Returns a single device object (same shape as the list item above).

**Response — 404 Not Found**
```json
{"error": "device not found"}
```

---

### GET /api/v1/devices/:id/health

Get real-time health metrics for a device.

**Response — 200 OK**
```json
{
  "cpu_usage": 12.5,
  "memory_usage": 48.3,
  "battery_level": 87,
  "disk_free_bytes": 10737418240,
  "temperature": 34.0
}
```

**Response — 404 Not Found**
```json
{"error": "device not found"}
```

---

### POST /api/v1/devices/:id/wake

Send a wake-screen command to the device.

**Request body**: none

**Response — 200 OK**
```json
{"message": "wake command sent"}
```

**Response — 404 Not Found**
```json
{"error": "device not found"}
```

**Response — 409 Conflict** (device is offline)
```json
{"error": "device emulator-5554 is offline"}
```

---

### POST /api/v1/devices/:id/reboot

Initiate a device reboot. Returns immediately; the reboot continues asynchronously.

**Request body**: none

**Response — 202 Accepted**
```json
{"message": "reboot initiated"}
```

**Response — 404 Not Found**
```json
{"error": "device not found"}
```

---

## Jobs

### POST /api/v1/jobs

Create and immediately schedule a new job. Scheduling and execution happen asynchronously after the response is returned.

**Request body**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `test_command` | string | yes | Shell command to run on each device via `/bin/sh -c` |
| `install_command` | string | no | Shell command to run before `test_command` (e.g. download/install artifacts). If it fails, `test_command` is skipped. |
| `strategy` | string | no | Execution strategy. Only `"fan-out"` (or empty) is accepted. |
| `device_filter` | object | no | Filter criteria for device selection (see below) |
| `artifact_path` | string | no | Path on the device to collect artifacts from |
| `timeout_minutes` | int | no | Per-device timeout. Falls back to server default (30 min). |

**device_filter object**

| Field | Type | Description |
|-------|------|-------------|
| `platform` | string | `"android"` or `"ios"` |
| `tags` | array of strings | Device must have all listed tags |

```json
{
  "test_command": "adb -s $FARMHAND_DEVICE_SERIAL shell am instrument -w com.example.test/androidx.test.runner.AndroidJUnitRunner",
  "install_command": "adb -s $FARMHAND_DEVICE_SERIAL install -r /tmp/build/app.apk && adb -s $FARMHAND_DEVICE_SERIAL install -r /tmp/build/test.apk",
  "strategy": "fan-out",
  "device_filter": {
    "platform": "android",
    "tags": ["production"]
  },
  "timeout_minutes": 60
}
```

**Response — 201 Created**

Returns the created job record.

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "test_command": "pytest /data/tests/ --tb=short",
  "strategy": "fan-out",
  "device_filter": {"platform": "android", "tags": ["production"]},
  "artifact_path": "",
  "timeout_minutes": 60,
  "status": "queued",
  "created_at": "2026-02-27T10:00:00Z",
  "started_at": null,
  "completed_at": null
}
```

**Job status values**: `queued`, `running`, `preparing`, `installing`, `completed`, `failed`, `cancelled`

**Response — 422 Unprocessable Entity** (missing `test_command`)
```json
{
  "error": "validation failed",
  "fields": {"test_command": "test_command is required"}
}
```

**Response — 422 Unprocessable Entity** (unsupported strategy)
```json
{"error": "unsupported strategy: shard"}
```

**Response — 500 Internal Server Error** (no available devices match the filter)
```json
{"error": "no available devices match the job filter"}
```

---

### GET /api/v1/jobs

List jobs. Returns at most 100 results, sorted by `created_at` descending.

**Query parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `status` | string | Filter by status: `queued`, `running`, `completed`, or `failed` |

**Response — 200 OK**

Returns an array of job objects (empty array when no results, never null).

```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "test_command": "pytest /data/tests/ --tb=short",
    "status": "completed",
    "created_at": "2026-02-27T10:00:00Z",
    "started_at": "2026-02-27T10:00:05Z",
    "completed_at": "2026-02-27T10:12:30Z"
  }
]
```

---

### GET /api/v1/jobs/:id

Get a single job with its per-device execution results.

**Response — 200 OK**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "test_command": "pytest /data/tests/ --tb=short",
  "status": "completed",
  "created_at": "2026-02-27T10:00:00Z",
  "started_at": "2026-02-27T10:00:05Z",
  "completed_at": "2026-02-27T10:12:30Z",
  "results": [
    {
      "id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
      "job_id": "550e8400-e29b-41d4-a716-446655440000",
      "device_id": "device-uuid",
      "status": "passed",
      "exit_code": 0,
      "duration_seconds": 745,
      "log_path": "./logs/550e8400/device-uuid.log",
      "artifacts": "[]",
      "error_message": "",
      "created_at": "2026-02-27T10:00:05Z"
    }
  ]
}
```

**Job result fields**

| Field | Type | Description |
|-------|------|-------------|
| `id` | string (UUID) | Unique identifier for this result row |
| `job_id` | string (UUID) | ID of the parent job |
| `device_id` | string (UUID) | ID of the device that ran the test |
| `status` | string | Result status (see values below) |
| `exit_code` | int | Process exit code from the test command |
| `duration_seconds` | int | Wall-clock seconds the command ran |
| `log_path` | string | Server-side path to the raw log file |
| `artifacts` | string | JSON array of artifact filenames |
| `error_message` | string | Human-readable failure description. Always present. Empty string `""` when the device passed; populated with the last output lines or a summary when status is `failed` or `error`. Never `null`. |
| `created_at` | string (RFC 3339) | Timestamp when the result row was created |

**Job result status values**: `running`, `passed`, `failed`, `error`

**Response — 404 Not Found**
```json
{"error": "job not found"}
```

---

### DELETE /api/v1/jobs/:id

Cancel a job by setting its status to `cancelled`. Returns HTTP 204 with no body.

**Response — 204 No Content**

**Response — 404 Not Found**
```json
{"error": "job not found"}
```

---

## Logs

### GET /api/v1/jobs/:id/status

Lightweight status poll for a job. Returns only the status fields, not the full results list.

**Response — 200 OK**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "created_at": "2026-02-27T10:00:00Z",
  "started_at": "2026-02-27T10:00:05Z",
  "completed_at": null
}
```

**Response — 404 Not Found**
```json
{"error": "job not found"}
```

---

### GET /api/v1/jobs/:id/logs

Stream job log output as Server-Sent Events (SSE). Streams logs from all devices that ran the job.

**Response headers**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

**SSE event format**

Each log line:
```
data: <log line text>\n\n
```

Terminal event (stream is complete):
```
event: done
data: {}

```

The stream stays open until the job finishes or the client disconnects. When the client disconnects, the server-side goroutine is cancelled — no goroutine leak.

**Response — 404 Not Found** (before SSE headers are written)
```json
{"error": "job not found"}
```

---

### GET /api/v1/jobs/:id/logs/:device_id

Stream log output for a single device within a job as Server-Sent Events (SSE).

**Path parameters**

| Parameter | Description |
|-----------|-------------|
| `id` | Job UUID |
| `device_id` | Device UUID. Must correspond to a `JobResult` row for the given job. |

**Response headers**
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

**SSE event format**

Each log line:
```
data: <log line text>\n\n
```

Terminal event (stream is complete):
```
event: done
data: {}

```

For a **completed/failed/cancelled** job the full log is read from disk, each line is emitted as a `data:` event, and the `done` event is sent immediately — no client disconnect required.

For a **running** job the stream is live-tailed. The stream closes with the `done` event when the job finishes or when the client disconnects.

**Response — 404 Not Found** (job does not exist)
```json
{"error": "job not found"}
```

**Response — 404 Not Found** (device has no result row for this job)
```json
{"error": "device log not found"}
```

**Example**

```
GET /api/v1/jobs/550e8400-e29b-41d4-a716-446655440000/logs/6ba7b810-9dad-11d1-80b4-00c04fd430c8
```

```
data: Installing test runner...\n\n
data: Running pytest /data/tests/ --tb=short\n\n
data: 2 passed in 12.3s\n\n
event: done
data: {}\n\n
```

---

## Artifacts

### GET /api/v1/jobs/:id/artifacts

List all artifact files collected from all devices that ran the job.

**Response — 200 OK**

Returns an array (empty when no artifacts).

```json
[
  {
    "filename": "test-results.xml",
    "size_bytes": 48320,
    "mime_type": "application/xml"
  },
  {
    "filename": "screenshot.png",
    "size_bytes": 204800,
    "mime_type": "image/png"
  }
]
```

**Response — 404 Not Found**
```json
{"error": "job not found"}
```

---

### GET /api/v1/jobs/:id/artifacts/*filepath

Download an artifact file by name. Streams the file bytes with the appropriate `Content-Type` and a `Content-Disposition: attachment` header.

**Path traversal protection**: Filenames containing `..`, null bytes, or percent-encoded `/` or `.` sequences are rejected.

**Response — 200 OK**

File bytes streamed with:
```
Content-Type: <detected MIME type>
Content-Disposition: attachment; filename="<filename>"
```

**Response — 400 Bad Request** (invalid filename)
```json
{"error": "invalid filename"}
```

**Response — 404 Not Found**
```json
{"error": "job not found"}
```
or
```json
{"error": "artifact not found"}
```

---

## System

### GET /api/v1/config

Return the running server configuration. The `auth_token` field is masked as `"***"` (empty when auth is disabled).

**Response — 200 OK**
```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080,
    "auth_token": "***",
    "cors_origins": ["*"],
    "dev_mode": false
  },
  "database": {
    "path": "farmhand.db",
    "retention_days": 30
  },
  "devices": {
    "auto_discover": true,
    "poll_interval_seconds": 5,
    "min_battery_percent": 20,
    "cleanup_between_runs": true,
    "wake_before_test": true,
    "adb_path": "adb"
  },
  "jobs": {
    "default_timeout_minutes": 30,
    "max_concurrent_jobs": 3,
    "artifact_storage_path": "./artifacts",
    "result_storage_path": "./results",
    "log_dir": "./logs",
    "max_artifact_size_mb": 500
  },
  "notifications": {
    "webhook_url": "",
    "notify_on": ["failure", "completion"]
  }
}
```

---

### GET /api/v1/stats

Return aggregated counts of devices and jobs by status, queried live from the database.

**Response — 200 OK**
```json
{
  "devices": {
    "total": 5,
    "online": 3,
    "offline": 1,
    "busy": 1
  },
  "jobs": {
    "total": 42,
    "queued": 1,
    "running": 2,
    "completed": 37,
    "failed": 2
  }
}
```

---

## WebSocket

### GET /api/v1/ws

Upgrade HTTP to WebSocket. Auth via `Authorization: Bearer <token>` header or `?token=<token>` query parameter.

The server limits simultaneous connections to 100. Exceeding this returns:
```
HTTP 503 Service Unavailable
{"error": "too many connections"}
```

### Message format

All messages use a JSON envelope:
```json
{
  "type": "<message type>",
  "payload": <object>
}
```

### Server-to-client message types

**`device_snapshot`** — sent once on connect with all current devices.
```json
{
  "type": "device_snapshot",
  "payload": [ ...device objects... ]
}
```

**`device_update`** — sent when a device's status changes.
```json
{
  "type": "device_update",
  "payload": { ...device object... }
}
```

**`job_update`** — sent when a job's status changes (started, completed, failed).
```json
{
  "type": "job_update",
  "payload": { ...job object... }
}
```

### Client-to-server messages

The WebSocket endpoint does not process inbound messages. Clients may send pings to keep the connection alive; the server reads and discards all client messages. Client disconnect is detected by read errors on the server side.

---

## Error responses

All error responses use the following shape:

```json
{"error": "<human-readable message>"}
```

Validation errors may include an additional `fields` object:

```json
{
  "error": "validation failed",
  "fields": {
    "<field_name>": "<reason>"
  }
}
```

### Common HTTP status codes

| Status | Meaning |
|--------|---------|
| 200 | Success |
| 201 | Resource created |
| 202 | Accepted (async operation started) |
| 204 | Success, no response body |
| 400 | Bad request (invalid input) |
| 401 | Unauthorized (missing or invalid token) |
| 404 | Resource not found |
| 409 | Conflict (e.g. device is offline) |
| 422 | Unprocessable entity (validation failure) |
| 500 | Internal server error |
| 503 | Service unavailable (too many WebSocket connections) |

---

## Request tracing

Every request receives a unique `X-Request-ID` header in the response. If the client sends an `X-Request-ID` request header, the same value is echoed back.
