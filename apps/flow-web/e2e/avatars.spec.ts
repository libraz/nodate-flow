/**
 * Avatar proxy E2E.
 *
 * Verifies that the unified Avatar primitive integrates with the
 * auth-api `/avatars/{userId}` proxy endpoint across two surfaces:
 *
 *   1. Top-bar account-menu trigger renders an Avatar whose
 *      underlying <img src> matches the proxy URL pattern.
 *   2. When the proxy returns 404 (user has no uploaded avatar — the
 *      default state for a fresh tenant), the primitive falls back to
 *      the initials text without surfacing a console error.
 *   3. Timeline event-card uses the same proxy URL for the actor
 *      avatar of a real user-emitted event (`task.created`), and AI /
 *      system actors fall through to the initials placeholder because
 *      `actorUserId` is empty.
 *
 * All preconditions are seeded via REST (CLAUDE.md rule 7) and each
 * spec uses an isolated tenant created in this file so the parallel
 * harness cannot collide on actor-specific assertions. Console errors
 * surfaced during page navigation fail the spec.
 */

import { type ConsoleMessage, type Page, expect, test } from '@playwright/test';

import {
  AUTH_API_URL,
  type TestTenant,
  cleanupTenant,
  createTask,
  createTestTenant,
  injectAuth,
} from './fixtures/tenant';
import { checkA11y } from './helpers/a11y';

/**
 * Filter for console messages worth failing the test on.
 *
 * Browsers log `Failed to load resource: ... 404` at error level for
 * any <img> whose request 404s. The Avatar fallback under test is
 * *exactly* that case — the image fails, the primitive swaps in
 * initials, and no JS-side error is raised. We must therefore exclude
 * the resource-load 404 noise; everything else (uncaught exceptions,
 * React warnings, runtime errors logged via console.error) still
 * fails the spec.
 */
function isFailableConsoleError(msg: ConsoleMessage): boolean {
  if (msg.type() !== 'error') return false;
  const text = msg.text();
  // Network-level 404s for static assets (the avatar proxy) are
  // expected when the user has no uploaded avatar.
  if (text.includes('Failed to load resource') && text.includes('404')) return false;
  return true;
}

/** Collects failable console errors over the lifetime of a Page. */
function attachConsoleGuard(page: Page): { errors: string[] } {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (isFailableConsoleError(msg)) {
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

    // a11y on the rendered shell with the avatar visible.
    await checkA11y(page, ['color-contrast', 'region', 'landmark-complementary-is-top-level']);

    expect(guard.errors, 'no failable console errors').toEqual([]);
  });

  test('avatar 404 falls back to initials without console errors', async ({ page }) => {
    tenant = await createTestTenant();
    const guard = attachConsoleGuard(page);

    // Fresh tenants have NULL avatar_url, so /avatars/{userId} returns
    // 404 if the top-bar ever asked for it. /me does NOT emit an
    // avatarUrl in that case, so the Avatar primitive renders the
    // initials text directly. We assert both — no <img> is mounted
    // and the initials are visible — to lock in the contract.
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

    // Independently confirm the proxy itself returns 404 for the
    // same user, so the fallback contract isn't accidentally satisfied
    // by a misconfigured base URL.
    const proxyResp = await page.request.get(`${AUTH_API_URL}/avatars/${tenant.userId}`);
    expect(proxyResp.status(), 'expected 404 from avatar proxy').toBe(404);

    // Console must stay clean — the fallback path takes effect at
    // render time, not via an <img onError>, so no resource-level
    // 404 should ever fire either.
    expect(guard.errors, 'no failable console errors').toEqual([]);
  });

  test('timeline event-card renders actor avatar via proxy URL', async ({ page }) => {
    tenant = await createTestTenant();
    const guard = attachConsoleGuard(page);

    // Seed a `task.created` event; the workspace timeline picks it up
    // with actorUserId = tenant.userId so the EventCard mounts an
    // <img src=...> through the avatar proxy.
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

    // The event-card's actor avatar mounts an <img src=...> when
    // actorUserId is non-empty. With a NULL avatar_url the image
    // 404s but the src attribute we want to assert is set BEFORE the
    // network round-trip — so the URL pattern check is deterministic.
    const actorImg = eventRow.locator('img').first();
    await expect(actorImg).toHaveAttribute('src', avatarUrlPattern(tenant.userId), {
      timeout: 10_000,
    });

    // Console must stay clean of JS-level errors.
    expect(guard.errors, 'no failable console errors').toEqual([]);
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

/**
 * Uploads a 1×1 PNG as the tenant's avatar via the public upload
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
  // 1×1 transparent PNG. Smallest legal payload accepted by the
  // upload handler's image MIME sniff.
  const pngBytes = Uint8Array.from([
    0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
    0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
    0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
    0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
    0x42, 0x60, 0x82,
  ]);

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
