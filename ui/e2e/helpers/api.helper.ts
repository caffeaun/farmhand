/**
 * API helper for E2E tests.
 *
 * Provides typed wrappers around the FarmHand REST API so test setup and
 * teardown can be done via HTTP rather than by driving the UI.
 *
 * Usage:
 *   const api = new ApiHelper(request, 'http://localhost:8080');
 *   const job = await api.createJob({ test_command: 'echo hello' });
 *   await api.deleteJob(job.id);
 */

import { type APIRequestContext } from '@playwright/test';

export interface ApiJob {
  id: string;
  test_command: string;
  status: string;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
}

export interface ApiDevice {
  id: string;
  serial: string;
  model: string;
  platform: string;
  status: string;
  battery_level: number;
}

export class ApiHelper {
  private request: APIRequestContext;
  private baseURL: string;
  private token: string;

  constructor(request: APIRequestContext, baseURL: string, token = '') {
    this.request = request;
    this.baseURL = baseURL.replace(/\/$/, '');
    this.token = token;
  }

  private headers(): Record<string, string> {
    const h: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.token) h['Authorization'] = `Bearer ${this.token}`;
    return h;
  }

  async getHealth(): Promise<{ status: string; version: string; uptime_seconds: number }> {
    const resp = await this.request.get(`${this.baseURL}/api/v1/health`);
    return resp.json();
  }

  async listDevices(): Promise<ApiDevice[]> {
    const resp = await this.request.get(`${this.baseURL}/api/v1/devices`, {
      headers: this.headers(),
    });
    return resp.json();
  }

  async createJob(data: {
    test_command: string;
    timeout_minutes?: number;
    device_filter?: { platform?: string; tags?: string[] };
  }): Promise<ApiJob> {
    const resp = await this.request.post(`${this.baseURL}/api/v1/jobs`, {
      headers: this.headers(),
      data,
    });
    return resp.json();
  }

  async getJob(id: string): Promise<ApiJob> {
    const resp = await this.request.get(`${this.baseURL}/api/v1/jobs/${id}`, {
      headers: this.headers(),
    });
    return resp.json();
  }

  async listJobs(status?: string): Promise<ApiJob[]> {
    const url = status
      ? `${this.baseURL}/api/v1/jobs?status=${status}`
      : `${this.baseURL}/api/v1/jobs`;
    const resp = await this.request.get(url, { headers: this.headers() });
    return resp.json();
  }

  async deleteJob(id: string): Promise<void> {
    await this.request.delete(`${this.baseURL}/api/v1/jobs/${id}`, {
      headers: this.headers(),
    });
  }

  async getStats(): Promise<{
    devices: { total: number; online: number; offline: number; busy: number };
    jobs: { total: number; queued: number; running: number; completed: number; failed: number };
  }> {
    const resp = await this.request.get(`${this.baseURL}/api/v1/stats`, {
      headers: this.headers(),
    });
    return resp.json();
  }
}
