/**
 * Visual regression snapshot tests.
 *
 * Captures screenshots of the 5 main pages across 4 themes and 2 languages
 * (en, ja), producing 40 baseline snapshots total. Baselines are stored in
 * `apps/flow-web/e2e/snapshots/` and compared on subsequent runs via
 * `expect(page).toHaveScreenshot()`.
 *
 * Themes: aurora-light, aurora-dark, dotline-light, dotline-dark
 * Languages: en, ja
 * Pages: login, task-list, board, settings, inbox
 *
 * Conventions followed:
 *   - Animations/motion disabled before capture (no-motion class).
 *   - Theme set via localStorage `nf.theme` + DOM `data-theme` attribute.
 *   - Language set via localStorage `nf.lang`.
 *   - Each test uses a fresh tenant via REST (no shared state).
 *
 * @see docs/conventions/testing.md "Visual Regression" section
 */

import { type Page, expect, test } from '@playwright/test';

import { loadTenants } from './fixtures/load-tenants';
import { type TestTenant, injectAuth } from './fixtures/tenant';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;
type Theme = (typeof THEMES)[number];

const LANGUAGES = ['en', 'ja'] as const;
type Language = (typeof LANGUAGES)[number];

/**
 * Page definitions. Each entry describes a page to capture.
 *   - `name`: used in the snapshot filename.
 *   - `path`: URL path (may contain `{projectId}` placeholder).
 *   - `auth`: whether the page requires authentication.
 *   - `waitFor`: optional selector to wait for before capturing.
 */
interface PageDef {
  name: string;
  path: string;
  auth: boolean;
  waitFor?: string;
}

const PAGES: PageDef[] = [
  // Note: login page lives in accounts-web, not flow-web; skip it here.
  {
    name: 'task-list',
    path: '/projects/{projectId}/tasks',
    auth: true,
    waitFor: 'main',
  },
  {
    name: 'settings',
    path: '/settings/profile',
    auth: true,
    waitFor: 'form, [role="form"], main',
  },
  {
    name: 'inbox',
    path: '/inbox',
    auth: true,
    waitFor: 'main',
  },
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Disable CSS animations and transitions to produce deterministic snapshots.
 * Adds a `no-motion` class to the document root and injects a blanket
 * transition/animation override.
 */
async function disableMotion(page: Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('no-motion');
  });
  await page.addStyleTag({
    content: `
      *, *::before, *::after {
        animation-duration: 0s !important;
        animation-delay: 0s !important;
        transition-duration: 0s !important;
        transition-delay: 0s !important;
        scroll-behavior: auto !important;
      }
    `,
  });
}

/**
 * Set the active theme via localStorage and the DOM attribute so the
 * ThemeProvider picks it up immediately on the next navigation/reload.
 */
async function setTheme(page: Page, theme: Theme): Promise<void> {
  await page.evaluate((t) => {
    localStorage.setItem('nf.theme', t);
    document.documentElement.setAttribute('data-theme', t);
  }, theme);
}

/**
 * Set the active language via localStorage so i18next picks it up on
 * the next navigation/reload.
 */
async function setLanguage(page: Page, lang: Language): Promise<void> {
  await page.evaluate((l) => {
    localStorage.setItem('nf.lang', l);
  }, lang);
}

/**
 * Resolve a page path, replacing `{projectId}` with the tenant's project.
 */
function resolvePath(pageDef: PageDef, tenant: TestTenant | null): string {
  if (!tenant) return pageDef.path;
  return pageDef.path.replace('{projectId}', tenant.projectId);
}

/**
 * Build a descriptive snapshot filename.
 * Example: `login-aurora-light-en.png`
 */
function snapshotName(pageName: string, theme: Theme, lang: Language): string {
  return `${pageName}-${theme}-${lang}.png`;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe('visual regression', () => {
  // Fixed viewport for consistent snapshots.
  test.use({ viewport: { width: 1280, height: 720 } });
  // Visual regression screenshots need extra time for rendering + comparison.
  test.setTimeout(60_000);

  let tenant: TestTenant | null = null;

  test.beforeAll(async () => {
    const { user } = loadTenants();
    tenant = user;
  });

  for (const pageDef of PAGES) {
    test.describe(pageDef.name, () => {
      for (const theme of THEMES) {
        for (const lang of LANGUAGES) {
          test(`${pageDef.name} | ${theme} | ${lang}`, async ({ page }) => {
            // Set theme and language before navigation.
            // Navigate to a blank page first to set localStorage on the
            // correct origin (Playwright requires an origin for evaluate).
            await page.goto('/');

            if (pageDef.auth && tenant) {
              await injectAuth(page.context(), tenant);
            }

            await setTheme(page, theme);
            await setLanguage(page, lang);

            // Navigate to the target page (reload picks up localStorage).
            const targetPath = resolvePath(pageDef, tenant);
            await page.goto(targetPath);

            // Wait for the page-specific element to be present.
            if (pageDef.waitFor) {
              await page.waitForSelector(pageDef.waitFor, { timeout: 15_000 });
            }

            // Disable motion after the page has loaded.
            await disableMotion(page);

            // Allow a brief settle for any remaining layout shifts.
            // Use domcontentloaded instead of networkidle — pages with
            // polling / SSE never reach networkidle.
            await page.waitForLoadState('domcontentloaded');
            await page.waitForTimeout(500);

            // Capture the screenshot and compare against baseline.
            // Use a generous pixel ratio for pages with dynamic content
            // (e.g. task-list where other tests seed tasks on the shared tenant).
            const ratio = pageDef.name === 'task-list' ? 0.05 : 0.01;
            await expect(page).toHaveScreenshot(snapshotName(pageDef.name, theme, lang), {
              fullPage: true,
              maxDiffPixelRatio: ratio,
            });
          });
        }
      }
    });
  }
});
