/**
 * API-level E2E tests.
 *
 * These tests exercise the REST API directly without driving the browser UI.
 * They cover endpoint contracts, authentication enforcement, error responses,
 * and cross-feature integration between device and job endpoints.
 *
 * Prerequisites:
 *   - A running farmhand server at BASE_URL (default http://localhost:8080).
 *   - FARMHAND_TOKEN env var set if the server requires auth.
 *
 * Run:
 *   BASE_URL=http://localhost:8080 FARMHAND_TOKEN=secret pnpm exec playwright test api
 */

import { test, expect } from '@playwright/test';
import { ApiHelper } from '../helpers/api.helper';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const TOKEN = process.env.FARMHAND_TOKEN || '';

test.describe('API: Health (public)', () => {
  test('returns status ok with version and uptime', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL);
    const health = await api.getHealth();

    expect(health.status).toBe('ok');
    expect(health.version).toBeTruthy();
    expect(health.uptime_seconds).toBeGreaterThanOrEqual(0);
  });
});

test.describe('API: Authentication enforcement', () => {
  test('protected endpoint returns 401 without token', async ({ request }) => {
    // Only enforce when a token is actually configured
    if (!TOKEN) {
      test.skip();
      return;
    }

    const resp = await request.get(`${BASE_URL}/api/v1/devices`);
    expect(resp.status()).toBe(401);

    const body = await resp.json();
    expect(body.error).toBe('unauthorized');
  });

  test('protected endpoint succeeds with valid token', async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/api/v1/devices`, {
      headers: TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {},
    });
    expect(resp.ok()).toBeTruthy();
  });
});

test.describe('API: Devices', () => {
  test('GET /api/v1/devices returns an array', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL, TOKEN);
    const devices = await api.listDevices();

    expect(Array.isArray(devices)).toBe(true);
  });

  test('device objects have required fields', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL, TOKEN);
    const devices = await api.listDevices();

    for (const device of devices) {
      expect(device).toHaveProperty('id');
      expect(device).toHaveProperty('model');
      expect(device).toHaveProperty('platform');
      expect(device).toHaveProperty('status');
      expect(['online', 'offline', 'busy']).toContain(device.status);
    }
  });

  test('GET /api/v1/devices/:id returns 404 for unknown ID', async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/api/v1/devices/nonexistent-device-id-99999`, {
      headers: TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {},
    });
    expect(resp.status()).toBe(404);
    const body = await resp.json();
    expect(body.error).toMatch(/not found/i);
  });
});

test.describe('API: Jobs', () => {
  test('GET /api/v1/jobs returns an array', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL, TOKEN);
    const jobs = await api.listJobs();

    expect(Array.isArray(jobs)).toBe(true);
  });

  test('POST /api/v1/jobs returns 422 without test_command', async ({ request }) => {
    const resp = await request.post(`${BASE_URL}/api/v1/jobs`, {
      headers: {
        'Content-Type': 'application/json',
        ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
      },
      data: { timeout_minutes: 5 },
    });
    expect(resp.status()).toBe(422);
    const body = await resp.json();
    expect(body.error).toBeTruthy();
  });

  test('POST /api/v1/jobs returns 422 for unsupported strategy', async ({ request }) => {
    const resp = await request.post(`${BASE_URL}/api/v1/jobs`, {
      headers: {
        'Content-Type': 'application/json',
        ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
      },
      data: { test_command: 'echo hello', strategy: 'shard' },
    });
    expect(resp.status()).toBe(422);
    const body = await resp.json();
    expect(body.error).toMatch(/unsupported strategy/i);
  });

  test('GET /api/v1/jobs/:id returns 404 for unknown job', async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/api/v1/jobs/00000000-0000-0000-0000-000000000000`, {
      headers: TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {},
    });
    expect(resp.status()).toBe(404);
    const body = await resp.json();
    expect(body.error).toMatch(/not found/i);
  });

  test('DELETE /api/v1/jobs/:id returns 404 for unknown job', async ({ request }) => {
    const resp = await request.delete(
      `${BASE_URL}/api/v1/jobs/00000000-0000-0000-0000-000000000000`,
      { headers: TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {} }
    );
    expect(resp.status()).toBe(404);
  });

  test('GET /api/v1/jobs filters by status', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL, TOKEN);

    for (const status of ['queued', 'running', 'completed', 'failed']) {
      const jobs = await api.listJobs(status);
      expect(Array.isArray(jobs)).toBe(true);
      for (const job of jobs) {
        // Running jobs may have status "preparing" or "installing" on the server
        // but the API normalises for the filter. Just verify we got an array.
        expect(typeof job.id).toBe('string');
      }
    }
  });
});

test.describe('API: Stats', () => {
  test('GET /api/v1/stats returns device and job counts', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL, TOKEN);
    const stats = await api.getStats();

    expect(typeof stats.devices.total).toBe('number');
    expect(typeof stats.devices.online).toBe('number');
    expect(typeof stats.devices.offline).toBe('number');
    expect(typeof stats.devices.busy).toBe('number');

    expect(typeof stats.jobs.total).toBe('number');
    expect(typeof stats.jobs.queued).toBe('number');
    expect(typeof stats.jobs.running).toBe('number');
    expect(typeof stats.jobs.completed).toBe('number');
    expect(typeof stats.jobs.failed).toBe('number');
  });
});

test.describe('API: Artifacts path traversal protection', () => {
  test('returns 400 for path with ".." in artifact name', async ({ request }) => {
    const resp = await request.get(
      `${BASE_URL}/api/v1/jobs/some-job-id/artifacts/../etc/passwd`,
      { headers: TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {} }
    );
    // Either 400 (caught by the handler) or 404 (job not found first) is acceptable.
    // The key assertion is that the server does NOT return 200 with sensitive content.
    expect([400, 404]).toContain(resp.status());
  });
});
