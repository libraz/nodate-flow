import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { expect } from '@playwright/test';

/**
 * Run axe-core accessibility checks against the current page state.
 *
 * Violations are asserted to be empty so any a11y regression fails the
 * test immediately with a descriptive diff.
 *
 * @param page - Playwright page instance.
 * @param disableRules - Optional axe rule IDs to skip (e.g. for known
 *   upstream issues that are tracked separately).
 */
export async function checkA11y(page: Page, disableRules?: string[]): Promise<void> {
  const builder = new AxeBuilder({ page });

  if (disableRules && disableRules.length > 0) {
    builder.disableRules(disableRules);
  }

  const results = await builder.analyze();
  expect(results.violations).toEqual([]);
}
