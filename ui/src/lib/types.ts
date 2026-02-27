// ─── Device types ────────────────────────────────────────────────────────────

export type DeviceStatus = 'online' | 'offline' | 'busy';

export interface Device {
	id: string;
	serial: string;
	model: string;
	platform: string;
	os_version: string;
	status: DeviceStatus;
	battery_level: number;
	tags: string[];
	last_seen_at: string; // ISO 8601 timestamp
}

export interface DeviceHealth {
	cpu_usage: number;
	memory_usage: number;
	battery_level: number;
	disk_free_bytes: number;
	temperature: number;
}

// ─── Job types ────────────────────────────────────────────────────────────────

export type JobStatus = 'queued' | 'running' | 'completed' | 'failed';

export interface Job {
	id: string;
	test_command: string;
	device_filter: DeviceFilter | null;
	status: JobStatus;
	timeout_minutes: number;
	created_at: string;
	started_at: string | null;
	completed_at: string | null;
	results: JobResult[];
}

export type JobResultStatus = 'running' | 'passed' | 'failed' | 'error';

export interface JobResult {
	id: string;
	job_id: string;
	device_id: string;
	status: JobResultStatus;
	started_at: string | null;
	completed_at: string | null;
	exit_code: number | null;
	error_message: string | null;
}

export interface Artifact {
	filename: string;
	path: string;
	size_bytes: number;
	mime_type: string;
}

// ─── Stats ───────────────────────────────────────────────────────────────────

export interface StatsResponse {
	devices: {
		total: number;
		online: number;
		offline: number;
		busy: number;
	};
	jobs: {
		total: number;
		queued: number;
		running: number;
		completed: number;
		failed: number;
	};
}

// ─── WebSocket ────────────────────────────────────────────────────────────────

export interface WSMessage {
	type: string;
	payload: unknown;
}

// ─── API error ────────────────────────────────────────────────────────────────

export class ApiError extends Error {
	readonly status: number;

	constructor(message: string, status: number) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
	}
}

// ─── Filter / request types ──────────────────────────────────────────────────

export interface DeviceFilter {
	platform?: string;
	status?: DeviceStatus;
	tags?: string[];
}

export interface JobFilter {
	status?: JobStatus;
}

export interface CreateJobRequest {
	test_command: string;
	device_filter?: DeviceFilter;
	timeout_minutes?: number;
}

// ─── Health / Config ─────────────────────────────────────────────────────────

export interface HealthResponse {
	status: string;
	version: string;
	uptime_seconds: number;
}

export interface ServerConfig {
	host: string;
	port: number;
	auth_token: string;
	cors_origins: string[];
	dev_mode: boolean;
}

export interface DatabaseConfig {
	path: string;
	retention_days: number;
}

export interface DevicesConfig {
	auto_discover: boolean;
	poll_interval_seconds: number;
	min_battery_percent: number;
	cleanup_between_runs: boolean;
	wake_before_test: boolean;
	adb_path: string;
}

export interface JobsConfig {
	default_timeout_minutes: number;
	max_concurrent_jobs: number;
	artifact_storage_path: string;
	result_storage_path: string;
	log_dir: string;
	max_artifact_size_mb: number;
}

export interface NotificationsConfig {
	webhook_url: string;
	notify_on: string[];
}

export interface ConfigResponse {
	server: ServerConfig;
	database: DatabaseConfig;
	devices: DevicesConfig;
	jobs: JobsConfig;
	notifications: NotificationsConfig;
}

// ─── Legacy alias (kept for backwards-compat with existing components) ───────

/** @deprecated Use Device instead */
export interface Toast {
	id: string;
	message: string;
	type: 'success' | 'error';
}
