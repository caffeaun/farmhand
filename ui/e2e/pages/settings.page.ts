import { type Page, type Locator, expect } from '@playwright/test';
import { BasePage } from './base.page';

export class SettingsPage extends BasePage {
  readonly tokenInput: Locator;
  readonly saveButton: Locator;
  readonly successMessage: Locator;

  constructor(page: Page) {
    super(page);
    // Settings page stores a bearer token
    this.tokenInput = page.getByLabel(/token/i).first();
    this.saveButton = page.getByRole('button', { name: /save/i }).first();
    this.successMessage = page.getByRole('alert').or(page.getByText(/saved/i)).first();
  }

  async goto(): Promise<void> {
    await this.navigate('/settings');
    await this.waitForPageLoad();
  }

  async setToken(token: string): Promise<void> {
    await this.tokenInput.fill(token);
    await this.saveButton.click();
  }
}
