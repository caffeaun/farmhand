/**
 * Navigation and routing E2E tests.
 *
 * Verifies that the SvelteKit SPA routing works correctly when served from
 * the embedded go:embed file server.
 *
 * Prerequisites:
 *   - A running farmhand server at BASE_URL (default http://localhost:8080).
 *   - FARMHAND_TOKEN env var set if the server requires auth.
 *
 * Run:
 *   BASE_URL=http://localhost:8080 FARMHAND_TOKEN=secret pnpm exec playwright test navigation
 */

import { test, expect } from '@playwright/test';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const TOKEN = process.env.FARMHAND_TOKEN || '';

test.beforeEach(async ({ page }) => {
  if (TOKEN) {
    await page.addInitScript((token) => {
      localStorage.setItem('farmhand_token', token);
    }, TOKEN);
  }
});

test.describe('SPA routing', () => {
  test('root URL redirects to /devices', async ({ page }) => {
    await page.goto(BASE_URL);
    await page.waitForURL('**/devices');
    await expect(page).toHaveURL(/\/devices$/);
  });

  test('/devices renders without 404', async ({ page }) => {
    await page.goto(`${BASE_URL}/devices`);
    await expect(page).not.toHaveURL(/404/);
    await expect(page.getByRole('heading', { name: 'Devices' })).toBeVisible();
  });

  test('/jobs renders without 404', async ({ page }) => {
    await page.goto(`${BASE_URL}/jobs`);
    await expect(page).not.toHaveURL(/404/);
    await expect(page.getByRole('heading', { name: 'Jobs' })).toBeVisible();
  });

  test('/settings renders without 404', async ({ page }) => {
    await page.goto(`${BASE_URL}/settings`);
    await expect(page).not.toHaveURL(/404/);
    // Settings page should load (exact heading text depends on implementation)
    await expect(page.locator('h1, h2').first()).toBeVisible();
  });

  test('sidebar navigation links exist', async ({ page }) => {
    await page.goto(`${BASE_URL}/devices`);
    await expect(page.getByRole('link', { name: /devices/i })).toBeVisible();
    await expect(page.getByRole('link', { name: /jobs/i })).toBeVisible();
    await expect(page.getByRole('link', { name: /settings/i })).toBeVisible();
  });

  test('clicking Jobs in sidebar navigates to /jobs', async ({ page }) => {
    await page.goto(`${BASE_URL}/devices`);
    await page.getByRole('link', { name: /jobs/i }).first().click();
    await expect(page).toHaveURL(/\/jobs$/);
    await expect(page.getByRole('heading', { name: 'Jobs' })).toBeVisible();
  });

  test('clicking Devices in sidebar navigates to /devices', async ({ page }) => {
    await page.goto(`${BASE_URL}/jobs`);
    await page.getByRole('link', { name: /devices/i }).first().click();
    await expect(page).toHaveURL(/\/devices$/);
    await expect(page.getByRole('heading', { name: 'Devices' })).toBeVisible();
  });

  test('deep link /jobs/:id is handled by SPA router', async ({ page, request }) => {
    // Navigate to a non-existent job ID — the SPA should render the job detail
    // page (possibly showing a not-found state) rather than serving a hard 404.
    await page.goto(`${BASE_URL}/jobs/00000000-0000-0000-0000-000000000000`);

    // The page should load (200), not return a server 404.
    // The SPA will show its own error state.
    const title = await page.title();
    expect(title).not.toBe('');
    await expect(page.locator('body')).not.toBeEmpty();
  });
});
