/**
 * Health endpoint E2E tests.
 *
 * These tests hit the public /api/v1/health endpoint directly. They do not
 * require any auth token and serve as a smoke test that the server is up.
 */

import { test, expect } from '@playwright/test';
import { ApiHelper } from '../helpers/api.helper';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Health endpoint', () => {
  test('GET /api/v1/health returns ok status', async ({ request }) => {
    const api = new ApiHelper(request, BASE_URL);
    const health = await api.getHealth();

    expect(health.status).toBe('ok');
    expect(typeof health.version).toBe('string');
    expect(typeof health.uptime_seconds).toBe('number');
    expect(health.uptime_seconds).toBeGreaterThanOrEqual(0);
  });

  test('health page does not require auth token', async ({ request }) => {
    const resp = await request.get(`${BASE_URL}/api/v1/health`);
    expect(resp.ok()).toBeTruthy();
    expect(resp.status()).toBe(200);
  });
});
