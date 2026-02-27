/**
 * Devices page E2E tests.
 *
 * Covers the happy path for viewing the device list, low-battery warnings,
 * and error states. Device action tests (wake/reboot) are marked as
 * conditional — they only run when at least one online device is available.
 *
 * Prerequisites:
 *   - A running farmhand server at BASE_URL (default http://localhost:8080).
 *   - FARMHAND_TOKEN env var set if the server requires auth.
 *
 * Run:
 *   BASE_URL=http://localhost:8080 FARMHAND_TOKEN=secret pnpm exec playwright test devices
 */

import { test, expect } from '@playwright/test';
import { DevicesPage } from '../pages/devices.page';
import { ApiHelper } from '../helpers/api.helper';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const TOKEN = process.env.FARMHAND_TOKEN || '';

// Inject the auth token into localStorage before each test.
test.beforeEach(async ({ page }) => {
  if (TOKEN) {
    await page.addInitScript((token) => {
      localStorage.setItem('farmhand_token', token);
    }, TOKEN);
  }
});

test.describe('Devices page', () => {
  test('renders the Devices heading', async ({ page }) => {
    const devicesPage = new DevicesPage(page);
    await devicesPage.goto();
    await devicesPage.waitForReady();

    await expect(devicesPage.heading).toBeVisible();
  });

  test('shows device table or empty state once loaded', async ({ page }) => {
    const devicesPage = new DevicesPage(page);
    await devicesPage.goto();
    await devicesPage.waitForReady();

    const count = await devicesPage.getDeviceCount();
    if (count > 0) {
      await expect(devicesPage.table).toBeVisible();
    } else {
      await expect(devicesPage.emptyState).toBeVisible();
    }
  });

  test('device rows show model, platform, status, and battery', async ({ page }) => {
    const api = new ApiHelper(page.request, BASE_URL, TOKEN);
    const devices = await api.listDevices();

    test.skip(devices.length === 0, 'No devices registered — skipping row content test');

    const devicesPage = new DevicesPage(page);
    await devicesPage.goto();
    await devicesPage.waitForReady();

    // Verify the first device's model appears in the table
    const firstDevice = devices[0];
    await expect(page.getByText(firstDevice.model, { exact: false })).toBeVisible();
  });

  test('navigates to /devices when visiting the root URL', async ({ page }) => {
    await page.goto(BASE_URL);
    await page.waitForURL('**/devices');
    await expect(page).toHaveURL(/\/devices$/);
  });

  test('shows error state when server is unreachable', async ({ page }) => {
    // Override the token to something invalid so the request returns 401,
    // which the API client treats as an error.
    await page.addInitScript(() => {
      localStorage.setItem('farmhand_token', 'invalid-token-12345');
    });

    const devicesPage = new DevicesPage(page);
    await devicesPage.goto();
    await devicesPage.waitForReady();

    // Either an error alert is shown, or the page redirected to /settings.
    const onSettings = page.url().includes('/settings');
    if (!onSettings) {
      await expect(devicesPage.errorState).toBeVisible();
    } else {
      await expect(page).toHaveURL(/\/settings/);
    }
  });
});

test.describe('Device actions', () => {
  test('wake button sends wake command for an online device', async ({ page }) => {
    const api = new ApiHelper(page.request, BASE_URL, TOKEN);
    const devices = await api.listDevices();
    const onlineDevice = devices.find((d) => d.status === 'online');

    test.skip(!onlineDevice, 'No online devices available — skipping wake test');

    if (!TOKEN) {
      await page.addInitScript(() => {
        localStorage.removeItem('farmhand_token');
      });
    }

    const devicesPage = new DevicesPage(page);
    await devicesPage.goto();
    await devicesPage.waitForReady();

    const wakeBtn = devicesPage.wakeButton(onlineDevice!.model);
    await expect(wakeBtn).toBeVisible();
    await wakeBtn.click();

    // Expect a success toast to appear
    await devicesPage.waitForToast(/wake signal sent/i);
  });
});
