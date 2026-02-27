/**
 * Thin fetch wrapper for the FarmHand API.
 *
 * Base URL: /api/v1 (same-origin, relative)
 * Auth:     Authorization: Bearer <token> from localStorage key "farmhand_token"
 * Errors:   Throws ApiError for non-2xx responses with a .status property
 * 401:      Redirects to /settings unless already on /settings
 */

import type {
	Artifact,
	ConfigResponse,
	CreateJobRequest,
	Device,
	DeviceFilter,
	DeviceHealth,
	HealthResponse,
	Job,
	JobFilter,
	StatsResponse
} from '$lib/types';
import { ApiError } from '$lib/types';

const BASE = '/api/v1';

/** The localStorage key used to persist the bearer token. */
export const TOKEN_KEY = 'farmhand_token';

// ─── Internal helpers ────────────────────────────────────────────────────────

function getToken(): string | null {
	// localStorage is only available in the browser
	if (typeof localStorage === 'undefined') return null;
	return localStorage.getItem(TOKEN_KEY);
}

function buildHeaders(): Record<string, string> {
	const headers: Record<string, string> = {
		'Content-Type': 'application/json'
	};
	const token = getToken();
	if (token) {
		headers['Authorization'] = `Bearer ${token}`;
	}
	return headers;
}

function handleUnauthorized(): void {
	if (typeof window === 'undefined') return;
	// Avoid infinite redirect loop when already on /settings
	if (!window.location.pathname.startsWith('/settings')) {
		window.location.href = '/settings';
	}
}

function buildQuery(params: Record<string, string | string[] | undefined>): string {
	const search = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		if (value === undefined) continue;
		if (Array.isArray(value)) {
			for (const v of value) {
				search.append(key, v);
			}
		} else {
			search.set(key, value);
		}
	}
	const qs = search.toString();
	return qs ? `?${qs}` : '';
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	const url = `${BASE}${path}`;
	const response = await fetch(url, {
		...init,
		headers: {
			...buildHeaders(),
			...(init.headers as Record<string, string> | undefined)
		}
	});

	if (response.status === 401) {
		handleUnauthorized();
		throw new ApiError('Unauthorized', 401);
	}

	if (!response.ok) {
		let message = response.statusText || `HTTP ${response.status}`;
		try {
			const body = (await response.json()) as { error?: string; message?: string };
			message = body.error ?? body.message ?? message;
		} catch {
			// Body was not JSON — use statusText
		}
		throw new ApiError(message, response.status);
	}

	// 204 No Content — return undefined cast to T
	if (response.status === 204) {
		return undefined as T;
	}

	return response.json() as Promise<T>;
}

// ─── Devices ─────────────────────────────────────────────────────────────────

/** Fetch all devices, optionally filtered by platform, status, or tags. */
export function getDevices(filter?: DeviceFilter): Promise<Device[]> {
	const qs = filter
		? buildQuery({
				platform: filter.platform,
				status: filter.status,
				tags: filter.tags
			})
		: '';
	return request<Device[]>(`/devices${qs}`);
}

/** Fetch a single device by id. */
export function getDevice(id: string): Promise<Device> {
	return request<Device>(`/devices/${encodeURIComponent(id)}`);
}

/** Fetch real-time health metrics for a device. */
export function getDeviceHealth(id: string): Promise<DeviceHealth> {
	return request<DeviceHealth>(`/devices/${encodeURIComponent(id)}/health`);
}

/** Send a wake signal to a device. */
export function wakeDevice(id: string): Promise<void> {
	return request<void>(`/devices/${encodeURIComponent(id)}/wake`, { method: 'POST' });
}

/** Reboot a device. */
export function rebootDevice(id: string): Promise<void> {
	return request<void>(`/devices/${encodeURIComponent(id)}/reboot`, { method: 'POST' });
}

// ─── Jobs ─────────────────────────────────────────────────────────────────────

/** Fetch all jobs, optionally filtered by status. */
export function getJobs(filter?: JobFilter): Promise<Job[]> {
	const qs = filter?.status ? buildQuery({ status: filter.status }) : '';
	return request<Job[]>(`/jobs${qs}`);
}

/** Fetch a single job by id. */
export function getJob(id: string): Promise<Job> {
	return request<Job>(`/jobs/${encodeURIComponent(id)}`);
}

/** Create a new job. */
export function createJob(data: CreateJobRequest): Promise<Job> {
	return request<Job>('/jobs', {
		method: 'POST',
		body: JSON.stringify(data)
	});
}

/** Delete a job by id. */
export function deleteJob(id: string): Promise<void> {
	return request<void>(`/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** Fetch the list of artifacts for a job. */
export function getJobArtifacts(id: string): Promise<Artifact[]> {
	return request<Artifact[]>(`/jobs/${encodeURIComponent(id)}/artifacts`);
}

// ─── System ───────────────────────────────────────────────────────────────────

/** Fetch aggregated device and job statistics. */
export function getStats(): Promise<StatsResponse> {
	return request<StatsResponse>('/stats');
}

/** Fetch the server configuration. */
export function getConfig(): Promise<ConfigResponse> {
	return request<ConfigResponse>('/config');
}

/** Fetch server health status. */
export function getHealth(): Promise<HealthResponse> {
	return request<HealthResponse>('/health');
}
