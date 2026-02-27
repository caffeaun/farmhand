import { type Page, type Locator, expect } from '@playwright/test';
import { BasePage } from './base.page';

export class DevicesPage extends BasePage {
  readonly heading: Locator;
  readonly table: Locator;
  readonly deviceRows: Locator;
  readonly emptyState: Locator;
  readonly loadingState: Locator;
  readonly errorState: Locator;

  constructor(page: Page) {
    super(page);
    this.heading = page.getByRole('heading', { name: 'Devices' });
    this.table = page.getByRole('table');
    this.deviceRows = page.getByRole('row').filter({ hasNot: page.getByRole('columnheader') });
    this.emptyState = page.getByText('No devices registered');
    this.loadingState = page.getByRole('status', { name: /loading devices/i });
    this.errorState = page.getByRole('alert').filter({ hasText: /failed to load devices/i });
  }

  async goto(): Promise<void> {
    await this.navigate('/devices');
    await this.waitForPageLoad();
  }

  async waitForReady(): Promise<void> {
    await expect(this.loadingState).toBeHidden({ timeout: 15_000 });
  }

  async getDeviceCount(): Promise<number> {
    return this.deviceRows.count();
  }

  wakeButton(deviceModel: string): Locator {
    return this.page.getByRole('button', { name: `Wake ${deviceModel}` });
  }

  rebootButton(deviceModel: string): Locator {
    return this.page.getByRole('button', { name: `Reboot ${deviceModel}` });
  }

  async waitForToast(text: string | RegExp): Promise<void> {
    await expect(this.page.getByRole('alert').filter({ hasText: text })).toBeVisible();
  }
}
