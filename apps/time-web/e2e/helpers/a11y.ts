import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { expect } from '@playwright/test';

/**
 * Run axe-core accessibility checks against the current page state.
 */
export async function checkA11y(page: Page, disableRules?: string[]): Promise<void> {
  const builder = new AxeBuilder({ page });

  if (disableRules && disableRules.length > 0) {
    builder.disableRules(disableRules);
  }

  const results = await builder.analyze();
  expect(results.violations).toEqual([]);
}
