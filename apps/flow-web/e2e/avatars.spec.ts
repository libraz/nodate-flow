/**
 * Avatar proxy E2E.
 *
 * Verifies that the unified Avatar primitive integrates with the
 * auth-api `/avatars/{userId}` proxy endpoint across two surfaces:
 *
 *   1. Top-bar account-menu trigger renders an Avatar whose
 *      underlying <img src> matches the proxy URL pattern.
 *   2. A fresh user with no avatar renders initials without requesting
 *      the proxy; the direct proxy endpoint still returns 404.
 *   3. Timeline event cards use only an avatar URL returned by the API,
 *      and an uploaded image successfully decodes with its cache tag.
 *
 * All preconditions are seeded via REST (CLAUDE.md rule 7) and each
 * spec uses an isolated tenant created in this file so the parallel
 * harness cannot collide on actor-specific assertions. Console errors
 * surfaced during page navigation fail the spec.
 */

import { Buffer } from 'node:buffer';
import { deflateSync } from 'node:zlib';
import { expect, type Page, test } from '@playwright/test';

import {
  AUTH_API_URL,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
  type TestTenant,
} from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

/** Collects failable console errors over the lifetime of a Page. */
function attachConsoleGuard(page: Page): { errors: string[] } {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      errors.push(`[${msg.type()}] ${msg.text()}`);
    }
  });
  page.on('pageerror', (err) => {
    errors.push(`[pageerror] ${err.message}`);
  });
  return { errors };
}

/** Regex that matches the auth-api avatar proxy URL for a given user id. */
function avatarUrlPattern(userId: string): RegExp {
  // The fixture's AUTH_API_URL is the source of truth for the auth-api
  // origin used by the web app's bundler (VITE_AUTH_API_BASE_URL); both
  // default to http://localhost:8082 in dev.
  const escapedBase = AUTH_API_URL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // The proxy may append `?v=<hash>` cache-busters when the /me payload
  // returns one; allow optional querystring.
  return new RegExp(`^${escapedBase}/avatars/${userId}(?:\\?.*)?$`);
}

test.describe('avatar proxy integration', () => {
  let tenant: TestTenant | null = null;

  test.afterEach(async () => {
    if (tenant) {
      await cleanupTenant(tenant);
      tenant = null;
    }
  });

  test('top-bar avatar uses the auth-api proxy URL', async ({ page }) => {
    tenant = await createTestTenant();
    const guard = attachConsoleGuard(page);

    // Force the user to carry an avatar_url so /me returns one and the
    // top-bar Avatar mounts an <img> rather than the initials path.
    // We POST a tiny PNG via the upload endpoint so the recorded URL
    // is a storage key — that key triggers the /avatars/{userId} proxy
    // path in the /me response (auth-api/me.go cacheBustFromKey).
    //
    // The upload requires the auth-api to have an S3-compatible object
    // store wired up (NF_S3_ENDPOINT). Local dev shells without minio
    // running surface AUTH.AVATAR.STORAGE_UNAVAILABLE; in that case we
    // skip with a precondition reason (this is NOT flaky-suppression —
    // the environment lacks a hard prerequisite for this assertion).
    // CI provisions storage so this branch never trips there.
    const uploaded = await uploadAvatar(tenant);
    test.skip(
      !uploaded,
      'auth-api avatar storage is not configured (NF_S3_ENDPOINT). Start minio to exercise this path.',
    );

    await injectAuth(page.context(), tenant);
    await page.goto('/today');

    // Wait for the topbar to mount (its Suspense children settle after
    // /me returns).
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });
    const avatarTrigger = page.getByRole('button', { name: /open account menu/i });
    await expect(avatarTrigger).toBeVisible({ timeout: 10_000 });

    // The Avatar primitive renders an <img> only when src is provided.
    // Locate the <img> nested in the trigger and assert its src matches
    // the proxy URL pattern for THIS user.
    const avatarImg = avatarTrigger.locator('img');
    await expect(avatarImg).toHaveAttribute('src', avatarUrlPattern(tenant.userId), {
      timeout: 10_000,
    });

    // Sanity: the request actually goes out to the proxy and resolves
    // with 200 once we have an uploaded avatar.
    const proxyResp = await page.request.get(`${AUTH_API_URL}/avatars/${tenant.userId}`);
    expect(proxyResp.status(), `proxy GET -> ${proxyResp.status()}`).toBe(200);

    // The shell can mount before the Today route finishes loading.
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();

    // a11y on the rendered shell with the avatar visible.
    await checkA11y(page, ['color-contrast', 'region', 'landmark-complementary-is-top-level']);

    expect(guard.errors, 'no failable console errors').toEqual([]);
  });

  test('a fresh user has initials and makes no avatar request', async ({ page }) => {
    tenant = await createTestTenant();
    const guard = attachConsoleGuard(page);
    const avatarRequests: string[] = [];
    const avatarURL = avatarUrlPattern(tenant.userId);
    page.on('request', (request) => {
      if (avatarURL.test(request.url())) avatarRequests.push(request.url());
    });

    // Fresh tenants have no avatar, so /me omits avatarUrl and the
    // account-menu Avatar renders initials without contacting the proxy.
    await injectAuth(page.context(), tenant);
    await page.goto('/today');

    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });
    const avatarTrigger = page.getByRole('button', { name: /open account menu/i });
    await expect(avatarTrigger).toBeVisible({ timeout: 10_000 });

    // No <img> should be mounted because /me did not carry avatarUrl.
    await expect(avatarTrigger.locator('img')).toHaveCount(0);

    // The initials derived from the displayName "E2E User <suffix>"
    // are "EU" (first letter of first + last word, uppercase).
    // displayName matches the literal seeded by createTestTenant.
    const initials = computeInitials(tenant.displayName);
    await expect(avatarTrigger).toContainText(initials);

    expect(avatarRequests, 'a user without an avatar makes zero image requests').toEqual([]);

    // Keep the endpoint contract as a direct control. The UI must avoid
    // requesting this URL when no avatar was supplied.
    const proxyResp = await page.request.get(`${AUTH_API_URL}/avatars/${tenant.userId}`);
    expect(proxyResp.status(), 'expected 404 from avatar proxy').toBe(404);

    expect(guard.errors, 'no failable console errors').toEqual([]);
  });

  test('timeline event-card keeps initials when its actor has no avatar', async ({ page }) => {
    tenant = await createTestTenant();
    const guard = attachConsoleGuard(page);
    const avatarRequests: string[] = [];
    const avatarURL = avatarUrlPattern(tenant.userId);
    page.on('request', (request) => {
      if (avatarURL.test(request.url())) avatarRequests.push(request.url());
    });

    // Seed a `task.created` event. Its actor has no avatar URL, so the
    // event card should keep initials instead of asking the proxy.
    const taskTitle = `Avatar timeline ${Date.now().toString(36)}`;
    await createTask(tenant, taskTitle);

    await injectAuth(page.context(), tenant);
    await page.goto(`/workspaces/${tenant.workspaceId}/timeline`);

    // Wait for the timeline view shell.
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 15_000 });

    // Wait for the seeded event row to materialise. The translated
    // message ends with the actor display name; matching on the
    // displayName is robust against i18n template wording changes.
    const eventRow = page
      .locator('section[aria-label]')
      .filter({ hasText: tenant.displayName })
      .first();
    await expect(eventRow).toBeVisible({ timeout: 15_000 });

    const actorAvatars = eventRow.locator(`[role="img"][aria-label="${tenant.displayName}"]`);
    const actorAvatarCount = await actorAvatars.count();
    expect(
      actorAvatarCount,
      'the seeded actor must appear in the visible timeline group',
    ).toBeGreaterThan(0);
    const initials = computeTimelineInitials(tenant.displayName);
    for (let i = 0; i < actorAvatarCount; i++) {
      const actorAvatar = actorAvatars.nth(i);
      await expect(actorAvatar).toContainText(initials);
      await expect(actorAvatar.locator('img')).toHaveCount(0);
    }
    expect(avatarRequests, 'an actor without an avatar makes zero image requests').toEqual([]);

    expect(guard.errors, 'no failable console errors').toEqual([]);
  });

  test('timeline event-card loads an uploaded avatar with its cache tag', async ({ page }) => {
    tenant = await createTestTenant();
    const uploaded = await uploadAvatar(tenant);
    test.skip(
      !uploaded,
      'auth-api avatar storage is not configured (NF_S3_ENDPOINT). Start minio to exercise this path.',
    );

    const guard = attachConsoleGuard(page);
    const avatarResponse = page.waitForResponse(
      (response) =>
        avatarUrlPattern(tenant.userId).test(response.url()) && response.status() === 200,
      { timeout: 15_000 },
    );
    const taskTitle = `Avatar timeline uploaded ${Date.now().toString(36)}`;
    await createTask(tenant, taskTitle);
    await injectAuth(page.context(), tenant);
    await page.goto(`/workspaces/${tenant.workspaceId}/timeline`);

    const eventRow = page
      .locator('section[aria-label]')
      .filter({ hasText: tenant.displayName })
      .first();
    await expect(eventRow).toBeVisible({ timeout: 15_000 });
    const actorImages = eventRow.locator(`[role="img"][aria-label="${tenant.displayName}"] img`);
    const actorImageCount = await actorImages.count();
    expect(actorImageCount, 'uploaded user avatars must appear in the timeline').toBeGreaterThan(0);
    const response = await avatarResponse;
    expect(response.status()).toBe(200);

    for (let i = 0; i < actorImageCount; i++) {
      const actorImg = actorImages.nth(i);
      await expect(actorImg).toHaveAttribute('src', avatarUrlPattern(tenant.userId), {
        timeout: 10_000,
      });
      const avatarSrc = await actorImg.getAttribute('src');
      expect(avatarSrc).not.toBeNull();
      expect(new URL(avatarSrc ?? '').searchParams.get('v')).toMatch(/^[0-9a-f]{8}$/);
      await expect
        .poll(() => actorImg.evaluate((img: HTMLImageElement) => img.naturalWidth), {
          timeout: 10_000,
        })
        .toBeGreaterThan(0);
    }

    expect(guard.errors, 'no console errors while loading an uploaded image').toEqual([]);
  });
});

/**
 * Computes the same initials that `top-bar.tsx#initialsFrom` produces.
 *
 * For a `displayName` of "E2E User <suffix>" the helper picks the first
 * letter of the first and last whitespace-separated word, uppercases
 * them, and concatenates — yielding e.g. "E<S>" where <S> is the first
 * char of the random suffix. We mirror that logic here so the test
 * derives the expected text from the same source the UI uses, instead
 * of hard-coding a fragile string.
 */
function computeInitials(displayName: string): string {
  const parts = displayName.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return ((first + last).toUpperCase() || '?').trim();
}

/** Compute the initials used by EventCard from the first two name parts. */
function computeTimelineInitials(displayName: string): string {
  return displayName
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? '')
    .join('');
}

/**
 * Uploads a valid 8×8 PNG as the tenant's avatar via the public upload
 * endpoint. The auth-api stores it under
 * `avatars/<userId>/<attachmentId>.png` and points the user row's
 * avatar_url at that storage key, which makes /me emit a proxy URL on
 * subsequent calls.
 *
 * Returns `true` on success, and `false` only when the auth-api
 * reports `AUTH.AVATAR.STORAGE_UNAVAILABLE` (no S3 endpoint configured
 * — typical of a bare local dev shell with no minio running). Any
 * other failure is thrown so the spec surfaces the real cause.
 */
async function uploadAvatar(tenant: TestTenant): Promise<boolean> {
  const pngBytes = createAvatarPNG();

  const form = new FormData();
  form.append('file', new Blob([pngBytes], { type: 'image/png' }), 'avatar.png');

  const res = await fetch(`${AUTH_API_URL}/me/avatar`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${tenant.accessToken}`,
      accept: 'application/json',
    },
    body: form,
  });
  if (res.ok) return true;
  const body = await res.text();
  // 503 + AUTH.AVATAR.STORAGE_UNAVAILABLE is the deterministic signal
  // for "object store is not wired up", the only failure that should
  // gate the spec instead of failing it.
  if (res.status === 503 && body.includes('AUTH.AVATAR.STORAGE_UNAVAILABLE')) {
    return false;
  }
  throw new Error(`POST /me/avatar -> ${res.status} ${body}`);
}

/** Build a small valid RGBA PNG for the real auth-api upload endpoint. */
function createAvatarPNG(): Uint8Array {
  const width = 8;
  const height = 8;
  const header = new Uint8Array(13);
  const headerView = new DataView(header.buffer);
  headerView.setUint32(0, width);
  headerView.setUint32(4, height);
  header[8] = 8; // bit depth
  header[9] = 6; // RGBA

  const scanlines = new Uint8Array(height * (1 + width * 4));
  for (let y = 0; y < height; y++) {
    const row = y * (1 + width * 4);
    scanlines[row] = 0; // PNG filter: None
    for (let x = 0; x < width; x++) {
      const pixel = row + 1 + x * 4;
      scanlines[pixel] = 40;
      scanlines[pixel + 1] = 100;
      scanlines[pixel + 2] = 220;
      scanlines[pixel + 3] = 255;
    }
  }

  const signature = Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const chunks = [
    pngChunk('IHDR', header),
    pngChunk('IDAT', deflateSync(scanlines)),
    pngChunk('IEND', new Uint8Array()),
  ];
  const output = new Uint8Array(
    signature.length + chunks.reduce((length, chunk) => length + chunk.length, 0),
  );
  let offset = 0;
  output.set(signature, offset);
  offset += signature.length;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

/** Encode a PNG chunk with the CRC required by the decoder. */
function pngChunk(type: string, data: Uint8Array): Uint8Array {
  const typeBytes = Buffer.from(type, 'ascii');
  const chunk = new Uint8Array(12 + data.length);
  const view = new DataView(chunk.buffer);
  view.setUint32(0, data.length);
  chunk.set(typeBytes, 4);
  chunk.set(data, 8);
  view.setUint32(8 + data.length, pngCRC32(chunk.subarray(4, 8 + data.length)));
  return chunk;
}

/** PNG's reflected CRC-32 polynomial, applied to chunk type and data. */
function pngCRC32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ ((crc & 1) === 1 ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}
