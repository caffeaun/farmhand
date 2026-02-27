import { type Page, type Locator, expect } from '@playwright/test';
import { BasePage } from './base.page';

export class JobsPage extends BasePage {
  readonly heading: Locator;
  readonly newJobButton: Locator;
  readonly jobRows: Locator;
  readonly emptyState: Locator;
  readonly loadingState: Locator;

  // New-job panel
  readonly panel: Locator;
  readonly commandInput: Locator;
  readonly platformSelect: Locator;
  readonly tagsInput: Locator;
  readonly timeoutInput: Locator;
  readonly createButton: Locator;
  readonly cancelButton: Locator;
  readonly panelError: Locator;

  constructor(page: Page) {
    super(page);
    this.heading = page.getByRole('heading', { name: 'Jobs' });
    this.newJobButton = page.getByRole('button', { name: 'New Job' });
    this.jobRows = page.getByRole('row').filter({ hasNot: page.getByRole('columnheader') });
    this.emptyState = page.getByText(/No jobs yet/i);
    this.loadingState = page.getByLabel(/loading jobs/i);

    // Panel
    this.panel = page.getByRole('dialog', { name: 'New job' });
    this.commandInput = page.getByLabel('Test Command');
    this.platformSelect = page.getByLabel('Device Platform');
    this.tagsInput = page.getByLabel('Device Tags');
    this.timeoutInput = page.getByLabel('Timeout (minutes)');
    this.createButton = page.getByRole('button', { name: 'Create Job' });
    this.cancelButton = page.getByRole('button', { name: 'Cancel' });
    this.panelError = page.getByRole('alert').filter({ hasText: /failed to create/i });
  }

  async goto(): Promise<void> {
    await this.navigate('/jobs');
    await this.waitForPageLoad();
  }

  async waitForReady(): Promise<void> {
    await expect(this.loadingState).toBeHidden({ timeout: 15_000 });
  }

  async openNewJobPanel(): Promise<void> {
    await this.newJobButton.click();
    await expect(this.panel).toBeVisible();
  }

  async fillJobForm(options: {
    command: string;
    platform?: string;
    tags?: string;
    timeout?: number;
  }): Promise<void> {
    await this.commandInput.fill(options.command);
    if (options.platform) {
      await this.platformSelect.selectOption(options.platform);
    }
    if (options.tags) {
      await this.tagsInput.fill(options.tags);
    }
    if (options.timeout !== undefined) {
      await this.timeoutInput.fill(String(options.timeout));
    }
  }

  async submitJobForm(): Promise<void> {
    await this.createButton.click();
  }

  filterTab(label: string): Locator {
    return this.page.getByRole('tab', { name: label });
  }

  jobLink(idPrefix: string): Locator {
    return this.page.getByRole('link', { name: new RegExp(`^${idPrefix}`) });
  }

  deleteButton(idPrefix: string): Locator {
    return this.page.getByRole('button', { name: new RegExp(`delete job ${idPrefix}`, 'i') });
  }
}
