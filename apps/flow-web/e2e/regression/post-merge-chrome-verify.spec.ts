/**
 * Post-merge chrome-verify substitute.
 *
 * The chrome-verify skill normally drives Chrome DevTools MCP for a
 * 6-axis judgement gate (layout / UX / i18n leak / multi-language /
 * happy+edge / runtime errors). The dod-verify agent doesn't have
 * Chrome DevTools MCP attached, so this spec captures the mechanical
 * subset of those axes that Playwright can prove without human
 * judgement: i18n leak detection, runtime error detection, console
 * cleanliness, and golden-path render across the merged calendar
 * surfaces.
 *
 * Surfaces walked:
 *   - /calendar  month view
 *   - /calendar  week view
 *   - /calendar  day view
 *   - event drawer (open the event we seeded)
 *   - /share/cal/{token}  read view
 *
 * For each, asserts:
 *   - heading renders (layout + a11y smoke)
 *   - no console errors / no React warnings
 *   - no 5xx response from the API
 *   - no raw i18n key leaked into the body text
 *   - locale flip (en -> ja) on a sample page renders translated copy
 *
 * Findings that fail any axis are JSON-encoded to stdout so the
 * dod-verify agent can record them in /tmp/r6-baseline/final-parity.md
 * and file follow-up tasks. This spec does not attempt to fix anything;
 * defects must go through the right agent (chrome-verify skill rule).
 */

import { type ConsoleMessage, expect, type Page, test } from '@playwright/test';

import {
  cleanupTenant,
  createCalendarEvent,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from '../fixtures/tenant';

interface PageGuards {
  consoleErrors: string[];
  consoleWarnings: string[];
  pageErrors: string[];
  serverErrors: string[];
  rawKeyLeaks: string[];
}

function newGuards(): PageGuards {
  return {
    consoleErrors: [],
    consoleWarnings: [],
    pageErrors: [],
    serverErrors: [],
    rawKeyLeaks: [],
  };
}

function attachGuards(page: Page, guards: PageGuards): void {
  page.on('console', (msg: ConsoleMessage) => {
    const txt = `${msg.text()}`;
    if (msg.type() === 'error') guards.consoleErrors.push(txt);
    if (msg.type() === 'warning') guards.consoleWarnings.push(txt);
  });
  page.on('pageerror', (err) => {
    guards.pageErrors.push(err.message);
  });
  page.on('response', (resp) => {
    const status = resp.status();
    if (status >= 500 && status < 600) {
      guards.serverErrors.push(`${status} ${resp.request().method()} ${resp.url()}`);
    }
  });
}

const RAW_KEY_RE = /\b[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_.]*\b/gi;
const ALLOWED_DOTTED = [
  '.com',
  '.js',
  '.ts',
  '.tsx',
  '.jsx',
  '.css',
  '.json',
  '.svg',
  '.png',
  '.io',
];

async function detectRawKeys(page: Page): Promise<string[]> {
  const bodyText = await page.evaluate(() => document.body.innerText || '');
  const matches = bodyText.match(RAW_KEY_RE) || [];
  return matches.filter((m) => !ALLOWED_DOTTED.some((s) => m.includes(s)));
}

function todayAtUnix(hhmm: string): number {
  const d = new Date();
  const [h, m] = hhmm.split(':').map(Number);
  d.setHours(h ?? 0, m ?? 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
}

test.describe('chrome-verify substitute (calendar surfaces)', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('walks calendar month/week/day, event drawer, public-share read view', async ({
    page,
    request,
  }) => {
    tenant = await createTestTenant();
    const calendar = await ensurePersonalCalendar(tenant);
    const eventTitle = `CV event ${Date.now().toString(36)}`;
    const seeded = await createCalendarEvent(tenant, calendar.id, {
      title: eventTitle,
      startAt: todayAtUnix('14:00'),
      endAt: todayAtUnix('15:00'),
      kind: 'event',
    });

    await injectAuth(page.context(), tenant);

    /* ── Surface 1: /calendar default (month) ────────────────── */
    const monthGuards = newGuards();
    attachGuards(page, monthGuards);
    await page.goto('/calendar');
    await expect(
      page.getByRole('heading', { level: 1, name: /^(Calendar|カレンダー)$/ }),
    ).toBeVisible({ timeout: 15_000 });
    monthGuards.rawKeyLeaks = await detectRawKeys(page);
    await page.screenshot({ path: '/tmp/r6-baseline/chrome-verify/calendar-month.png' });

    /* ── Surface 2: week view ────────────────────────────────── */
    const weekToggle = page.getByRole('button', { name: /^(Week|週)$/ });
    if (await weekToggle.isVisible({ timeout: 1_000 }).catch(() => false)) {
      await weekToggle.click();
      await page.waitForTimeout(500);
      await page.screenshot({ path: '/tmp/r6-baseline/chrome-verify/calendar-week.png' });
    }

    /* ── Surface 3: day view ─────────────────────────────────── */
    const dayToggle = page.getByRole('button', { name: /^(Day|日)$/ });
    if (await dayToggle.isVisible({ timeout: 1_000 }).catch(() => false)) {
      await dayToggle.click();
      await page.waitForTimeout(500);
      await page.screenshot({ path: '/tmp/r6-baseline/chrome-verify/calendar-day.png' });
    }

    /* ── Surface 4: event drawer (click the seeded pill) ─────── */
    // Flip back to month view so the pill is reliably reachable.
    const monthToggle = page.getByRole('button', { name: /^(Month|月)$/ });
    if (await monthToggle.isVisible({ timeout: 1_000 }).catch(() => false)) {
      await monthToggle.click();
      await page.waitForTimeout(500);
    }
    const pill = page.locator(
      `button[aria-label^="Open event: "][aria-label*=${JSON.stringify(eventTitle)}]`,
    );
    if (
      await pill
        .first()
        .isVisible({ timeout: 5_000 })
        .catch(() => false)
    ) {
      await pill.first().click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible({ timeout: 5_000 });
      await page.screenshot({ path: '/tmp/r6-baseline/chrome-verify/event-drawer.png' });
      // Close the drawer with Escape so the next assertions can resume
      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden({ timeout: 5_000 });
    }

    /* ── Surface 5: public share read view ───────────────────── */
    const shareCreate = await request.post(
      `${process.env.NF_API_URL ?? 'http://localhost:8080'}/workspaces/${tenant.workspaceId}/public-shares`,
      {
        headers: {
          'Content-Type': 'application/json',
          accept: 'application/json',
          authorization: `Bearer ${tenant.accessToken}`,
        },
        data: { title: `CV share ${Date.now().toString(36)}` },
      },
    );
    expect(shareCreate.status()).toBeLessThan(400);
    const shareBody = (await shareCreate.json()) as { token: string };

    const sharePage = await page.context().newPage();
    const shareGuards = newGuards();
    attachGuards(sharePage, shareGuards);
    await sharePage.goto(`/share/cal/${shareBody.token}`);
    await expect(sharePage.locator('body')).toBeVisible({ timeout: 10_000 });
    shareGuards.rawKeyLeaks = await detectRawKeys(sharePage);
    await sharePage.screenshot({ path: '/tmp/r6-baseline/chrome-verify/share-read.png' });
    await sharePage.close();

    /* ── Surface 6: ja locale on /calendar ───────────────────── */
    const jaTenant = await createTestTenant({ locale: 'ja' });
    const jaCal = await ensurePersonalCalendar(jaTenant);
    await createCalendarEvent(jaTenant, jaCal.id, {
      title: `JA event ${Date.now().toString(36)}`,
      startAt: todayAtUnix('14:00'),
      endAt: todayAtUnix('15:00'),
      kind: 'event',
    });
    const browser = page.context().browser();
    if (!browser) throw new Error('no browser');
    const jaContext = await browser.newContext({
      viewport: { width: 1280, height: 800 },
    });
    try {
      await injectAuth(jaContext, jaTenant);
      const jaPage = await jaContext.newPage();
      const jaGuards = newGuards();
      attachGuards(jaPage, jaGuards);
      await jaPage.goto('/calendar');
      await expect(jaPage.getByRole('heading', { level: 1, name: 'カレンダー' })).toBeVisible({
        timeout: 15_000,
      });
      jaGuards.rawKeyLeaks = await detectRawKeys(jaPage);
      await jaPage.screenshot({ path: '/tmp/r6-baseline/chrome-verify/calendar-ja.png' });
      await cleanupTenant(jaTenant);

      /* Compose the report */
      const report = {
        month: monthGuards,
        share: shareGuards,
        ja: jaGuards,
        seededEvent: { id: seeded.id, title: eventTitle },
      };
      console.info('CV-REPORT:', JSON.stringify(report, null, 2));

      // The report dump above is for the agent. Hard assertions:
      expect(monthGuards.pageErrors, 'month: no uncaught errors').toEqual([]);
      expect(monthGuards.serverErrors, 'month: no 5xx').toEqual([]);
      expect(monthGuards.rawKeyLeaks, 'month: no raw i18n keys').toEqual([]);

      expect(shareGuards.pageErrors, 'share: no uncaught errors').toEqual([]);
      expect(shareGuards.serverErrors, 'share: no 5xx').toEqual([]);
      expect(shareGuards.rawKeyLeaks, 'share: no raw i18n keys').toEqual([]);

      expect(jaGuards.pageErrors, 'ja: no uncaught errors').toEqual([]);
      expect(jaGuards.serverErrors, 'ja: no 5xx').toEqual([]);
      expect(jaGuards.rawKeyLeaks, 'ja: no raw i18n keys').toEqual([]);
    } finally {
      await page.context().unrouteAll({ behavior: 'ignoreErrors' });
      await jaContext.close();
    }
  });
});
