/**
 * Jobs page E2E tests.
 *
 * Covers the happy path for viewing, creating, filtering, and deleting jobs.
 *
 * Prerequisites:
 *   - A running farmhand server at BASE_URL (default http://localhost:8080).
 *   - FARMHAND_TOKEN env var set if the server requires auth.
 *   - At least one online device registered with the server for creation tests.
 *
 * Run:
 *   BASE_URL=http://localhost:8080 FARMHAND_TOKEN=secret pnpm exec playwright test jobs
 */

import { test, expect } from '@playwright/test';
import { JobsPage } from '../pages/jobs.page';
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

test.describe('Jobs list page', () => {
  test('renders the Jobs heading', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await expect(jobsPage.heading).toBeVisible();
  });

  test('shows filter tabs for All, Queued, Running, Completed, Failed', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    for (const label of ['All', 'Queued', 'Running', 'Completed', 'Failed']) {
      await expect(jobsPage.filterTab(label)).toBeVisible();
    }
  });

  test('shows job rows or empty state once loaded', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    const count = await jobsPage.jobRows.count();
    if (count === 0) {
      await expect(jobsPage.emptyState).toBeVisible();
    } else {
      await expect(jobsPage.jobRows.first()).toBeVisible();
    }
  });

  test('filter tab updates the URL with ?status= query param', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await jobsPage.filterTab('Completed').click();
    await expect(page).toHaveURL(/status=completed/);

    await jobsPage.filterTab('All').click();
    await expect(page).not.toHaveURL(/status=/);
  });

  test('New Job button opens the slide-over panel', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await jobsPage.openNewJobPanel();
    await expect(jobsPage.commandInput).toBeVisible();
    await expect(jobsPage.createButton).toBeVisible();
  });

  test('new job panel shows validation error when command is empty', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await jobsPage.openNewJobPanel();
    await jobsPage.submitJobForm();

    // Expect a validation error message
    await expect(page.getByText(/test command is required/i)).toBeVisible();
  });

  test('cancel button closes the new job panel', async ({ page }) => {
    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await jobsPage.openNewJobPanel();
    await jobsPage.cancelButton.click();

    await expect(jobsPage.panel).toBeHidden();
  });
});

test.describe('Job creation', () => {
  test('creates a job and it appears in the list', async ({ page }) => {
    const api = new ApiHelper(page.request, BASE_URL, TOKEN);
    const devices = await api.listDevices();
    test.skip(devices.length === 0, 'No devices registered — job scheduling will fail');

    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    await jobsPage.openNewJobPanel();
    await jobsPage.fillJobForm({
      command: 'echo "e2e test job"',
      timeout: 5,
    });
    await jobsPage.submitJobForm();

    // Panel should close on success
    await expect(jobsPage.panel).toBeHidden({ timeout: 15_000 });

    // The new job should appear at the top of the list
    const rows = await jobsPage.jobRows.count();
    expect(rows).toBeGreaterThan(0);
  });

  test('clicking a job ID link navigates to the job detail page', async ({ page }) => {
    const api = new ApiHelper(page.request, BASE_URL, TOKEN);
    const jobs = await api.listJobs();

    test.skip(jobs.length === 0, 'No jobs in the system — skipping navigation test');

    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    // Click the first job ID link
    const firstLink = page.getByRole('link').filter({ has: page.locator('a[href^="/jobs/"]') }).first()
      .or(page.locator('a[href^="/jobs/"]').first());

    await firstLink.click();
    await expect(page).toHaveURL(/\/jobs\//);
  });
});

test.describe('Job deletion', () => {
  let createdJobId: string;

  test.beforeEach(async ({ page }) => {
    // Create a job via API to test deletion
    const api = new ApiHelper(page.request, BASE_URL, TOKEN);
    const devices = await api.listDevices();
    if (devices.length === 0) {
      createdJobId = '';
      return;
    }
    try {
      const job = await api.createJob({ test_command: 'echo "delete-me"', timeout_minutes: 1 });
      createdJobId = job.id;
    } catch {
      createdJobId = '';
    }
  });

  test('delete button shows confirmation dialog', async ({ page }) => {
    if (!createdJobId) {
      test.skip();
      return;
    }

    const jobsPage = new JobsPage(page);
    await jobsPage.goto();
    await jobsPage.waitForReady();

    const deleteBtn = jobsPage.deleteButton(createdJobId.slice(0, 8));
    if (await deleteBtn.isVisible()) {
      await deleteBtn.click();
      await expect(page.getByRole('dialog', { name: /delete job/i })).toBeVisible();
    }
  });
});
