import { type Page, type Locator, expect } from '@playwright/test';
import { BasePage } from './base.page';

export class JobDetailPage extends BasePage {
  readonly logViewer: Locator;
  readonly statusBadge: Locator;
  readonly artifactSection: Locator;
  readonly resultsSection: Locator;
  readonly doneIndicator: Locator;

  constructor(page: Page) {
    super(page);
    this.logViewer = page.getByTestId('log-viewer').or(page.locator('[aria-label*="log"]').first());
    this.statusBadge = page.locator('[class*="status"]').first();
    this.artifactSection = page.getByText(/Artifacts/i).first();
    this.resultsSection = page.getByText(/Results/i).first();
    this.doneIndicator = page.getByText(/completed|failed|cancelled/i).first();
  }

  async goto(jobId: string): Promise<void> {
    await this.navigate(`/jobs/${jobId}`);
    await this.waitForPageLoad();
  }

  async waitForJobStatus(status: string): Promise<void> {
    await expect(this.page.getByText(status, { exact: false })).toBeVisible({ timeout: 60_000 });
  }
}
