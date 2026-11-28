/**
 * Attachment image preview and gallery E2E.
 *
 * Uploads two PNGs and one PDF through the real upload flow (presign, PUT to
 * object storage, confirm) and checks the thumbnail rows, the lightbox
 * gallery, localized labels, focus return and the dark backdrop under a
 * light theme. Every test owns its tenant so the cases run in parallel.
 */

import { Buffer } from 'node:buffer';
import { deflateSync } from 'node:zlib';
import { expect, type Locator, test } from '@playwright/test';
import enCalendarEvents from '../locales/en/calendar-events.json' with { type: 'json' };
import enCommon from '../locales/en/common.json' with { type: 'json' };
import jaCommon from '../locales/ja/common.json' with { type: 'json' };
import {
  API_BASE_URL,
  AUTH_API_URL,
  cleanupTenant,
  createCalendarEvent,
  createTask,
  createTestTenant,
  ensurePersonalCalendar,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';

const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(buf: Buffer): number {
  let c = 0xffffffff;
  for (const b of buf) c = (CRC_TABLE[(c ^ b) & 0xff] as number) ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

/** Builds a valid size x size solid-colour RGB PNG. */
function makePng(size: number, [r, g, b]: [number, number, number]): Buffer {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // RGB
  const row = Buffer.concat([Buffer.from([0]), Buffer.from(Array(size).fill([r, g, b]).flat())]);
  const raw = Buffer.concat(Array(size).fill(row));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

const PDF_BYTES = Buffer.from(
  '%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n',
);

interface Upload {
  name: string;
  mimeType: string;
  buffer: Buffer;
}

/** Distinct bytes per file so content de-duplication never merges them. */
function files(tag: string): { red: Upload; blue: Upload; pdf: Upload } {
  return {
    red: { name: `${tag}-red.png`, mimeType: 'image/png', buffer: makePng(48, [200, 30, 30]) },
    blue: { name: `${tag}-blue.png`, mimeType: 'image/png', buffer: makePng(64, [30, 30, 200]) },
    pdf: { name: `${tag}-doc.pdf`, mimeType: 'application/pdf', buffer: PDF_BYTES },
  };
}

/** Sets the user's stored theme so the app converges to it on load. */
async function setTheme(tenant: TestTenant, theme: string): Promise<void> {
  const res = await fetch(`${AUTH_API_URL}/me`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ themePreference: theme }),
  });
  if (!res.ok) throw new Error(`PATCH /me -> ${res.status} ${await res.text()}`);
}

/** Uploads each file through the hidden input and waits for its row. */
async function uploadAll(container: Locator, uploads: Upload[]): Promise<void> {
  for (const file of uploads) {
    await container.locator('input[type="file"]').setInputFiles(file);
    await expect(container.getByText(file.name, { exact: true })).toBeVisible({ timeout: 15_000 });
  }
}

function thumbButton(container: Locator, filename: string, openPreview: string): Locator {
  return container.getByRole('button', { name: openPreview.replace('{filename}', filename) });
}

/** The image row's slot: the thumbnail button for images, a bare aria-hidden span otherwise. */
function rowOf(container: Locator, filename: string): Locator {
  return container
    .getByRole('listitem')
    .filter({ has: container.page().getByText(filename, { exact: true }) });
}

async function expectLoaded(img: Locator): Promise<void> {
  await expect
    .poll(() => img.evaluate((el) => (el as HTMLImageElement).naturalWidth), { timeout: 15_000 })
    .toBeGreaterThan(0);
}

test.describe('attachment image gallery', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  for (const locale of ['en', 'ja'] as const) {
    test(`task: thumbnails load and the gallery opens, navigates, zooms and returns focus (${locale})`, async ({
      page,
    }) => {
      const messages = locale === 'en' ? enCommon : jaCommon;
      const labels = messages.gallery.labels;
      const openPreview = messages.gallery.open_preview;
      tenant = await createTestTenant({ locale });
      const task = await createTask(tenant, `Gallery task ${locale}`);
      const f = files(`t-${locale}`);
      // A light theme must not lighten the gallery.
      await setTheme(tenant, 'glass-light');
      await injectAuth(page.context(), tenant);

      await page.goto(`/tasks/${task.id}`);
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'glass-light', {
        timeout: 15_000,
      });
      const panel = page.locator('#main-content');
      await uploadAll(panel, [f.red, f.blue, f.pdf]);

      // Thumbnails: both images load from the API origin's /blobs path.
      const apiOrigin = new URL(API_BASE_URL).origin;
      for (const file of [f.red, f.blue]) {
        const row = rowOf(panel, file.name);
        const button = thumbButton(row, file.name, openPreview);
        await expect(button).toBeVisible();
        const img = button.locator('img');
        await expectLoaded(img);
        const src = (await img.getAttribute('src')) ?? '';
        expect(new URL(src).origin).toBe(apiOrigin);
        expect(new URL(src).pathname).toMatch(/^\/(?:.*\/)?blobs\//);
      }

      // PDF: inert slot, no button and no image.
      const pdfRow = rowOf(panel, f.pdf.name);
      await expect(pdfRow.locator('img')).toHaveCount(0);
      await expect(
        pdfRow.getByRole('button', { name: openPreview.replace('{filename}', f.pdf.name) }),
      ).toHaveCount(0);

      // The list order decides the second thumbnail; click the second image row's button.
      const thumbs = panel.locator('button[data-status="ready"]');
      await expect(thumbs).toHaveCount(2);
      const opener = thumbs.nth(1);
      const openerName = (await opener.getAttribute('aria-label')) ?? '';
      await opener.click();

      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      const counter = dialog.locator('.yarl__counter');
      const total = 2;
      await expect(counter).toHaveText(`2 / ${total}`);
      // The caption shows the opened file's name (second image in list order).
      const openedName = openerName.replace(openPreview.replace('{filename}', ''), '');
      await expect(dialog.getByText(openedName, { exact: true }).first()).toBeVisible();

      // Button set carries the localized names.
      for (const name of [
        labels.previous,
        labels.next,
        labels.close,
        labels.zoom_in,
        labels.rotate_cw,
        labels.rotate_ccw,
        labels.enter_fullscreen,
        labels.play,
        labels.download,
      ]) {
        await expect(dialog.getByRole('button', { name, exact: true })).toBeVisible();
      }
      if (locale === 'ja') {
        for (const en of ['Previous', 'Next', 'Close', 'Zoom in', 'Download']) {
          await expect(dialog.getByRole('button', { name: en, exact: true })).toHaveCount(0);
        }
      }

      // Dark backdrop under a light theme.
      const backdrop = await dialog.locator('.yarl__container').evaluate((el) => {
        const m = getComputedStyle(el).backgroundColor.match(/[\d.]+/g) ?? [];
        return m.map(Number);
      });
      const [r = 255, g = 255, b = 255] = backdrop;
      expect(r + g + b).toBeLessThan(3 * 80);
      const tokenOnPortal = await dialog.evaluate((el) =>
        getComputedStyle(el).getPropertyValue('--nf-color-media-backdrop').trim(),
      );
      expect(tokenOnPortal).not.toBe('');

      // Navigation moves the counter in both directions.
      await page.keyboard.press('ArrowLeft');
      await expect(counter).toHaveText(`1 / ${total}`);
      await dialog.getByRole('button', { name: labels.next, exact: true }).click();
      await expect(counter).toHaveText(`2 / ${total}`);
      await dialog.getByRole('button', { name: labels.previous, exact: true }).click();
      await expect(counter).toHaveText(`1 / ${total}`);

      // Zoom: zoom-out is disabled at 1x and becomes available after zooming in.
      const zoomOut = dialog.getByRole('button', { name: labels.zoom_out, exact: true });
      await expect(zoomOut).toBeDisabled();
      await dialog.getByRole('button', { name: labels.zoom_in, exact: true }).click();
      await expect(zoomOut).toBeEnabled();

      // Escape closes and focus returns to the clicked thumbnail.
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
      await expect(panel.getByRole('button', { name: openerName, exact: true })).toBeFocused();
    });
  }

  test('event: thumbnail opens the gallery and Escape returns focus', async ({ page }) => {
    const labels = enCommon.gallery.labels;
    tenant = await createTestTenant();
    const calendar = await ensurePersonalCalendar(tenant);
    const start = Math.floor(Date.now() / 1000) + 3600;
    const event = await createCalendarEvent(tenant, calendar.id, {
      title: 'Gallery event',
      startAt: start,
      endAt: start + 3600,
    });
    const f = files('ev');
    await injectAuth(page.context(), tenant);

    await page.goto(
      `/workspaces/${tenant.workspaceId}/calendars/${calendar.id}/events/${event.id}`,
    );
    await page.getByRole('tab', { name: enCalendarEvents.event.detail.tab.attachments }).click();
    const panel = page.getByRole('tabpanel');
    await uploadAll(panel, [f.red, f.blue, f.pdf]);

    const thumbs = panel.locator('button[data-status="ready"]');
    await expect(thumbs).toHaveCount(2);
    await expectLoaded(thumbs.first().locator('img'));
    const opener = thumbs.nth(1);
    const openerName = (await opener.getAttribute('aria-label')) ?? '';
    await opener.click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.yarl__counter')).toHaveText('2 / 2');
    await dialog.getByRole('button', { name: labels.previous, exact: true }).click();
    await expect(dialog.locator('.yarl__counter')).toHaveText('1 / 2');

    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(panel.getByRole('button', { name: openerName, exact: true })).toBeFocused();
  });
});
