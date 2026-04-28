/**
 * /setup first-run onboarding e2e.
 *
 * The setup route is the workspace-creation surface for an authenticated
 * user who has no workspace yet. The route loader redirects to /calendar
 * when the user already belongs to one, so this spec registers a
 * brand-new auth-only user (no workspace) and drives the form end to
 * end:
 *
 *   1. Register a fresh user via POST /auth/register on auth-api.
 *   2. Inject the session into the browser context.
 *   3. Navigate to /setup → form is visible because the user has no
 *      workspaces.
 *   4. Fill name → slug autocompletes from the slugified name.
 *   5. Submit → backend creates the workspace and the SPA navigates to
 *      /calendar.
 *
 * Fresh user is created per-test so reruns are deterministic — the
 * shared tenant pool from `globalSetup` already has workspaces and
 * cannot be used for this surface.
 */

import { type BrowserContext, expect, test } from '@playwright/test';

import enCommon from '../locales/en/common.json' with { type: 'json' };
import { AUTH_API_URL } from './fixtures/tenant';

const copy = {
  title: enCommon.workspaces.setup.title,
  description: enCommon.workspaces.setup.description,
  namePlaceholder: enCommon.workspaces.setup.name_placeholder,
  slugPlaceholder: enCommon.workspaces.setup.slug_placeholder,
  submit: enCommon.workspaces.setup.submit,
} as const;

interface FreshUser {
  email: string;
  password: string;
  accessToken: string;
}

async function registerWorkspaceless(): Promise<FreshUser> {
  const suffix = Math.random().toString(36).slice(2, 14);
  const email = `setup+${suffix}@example.test`;
  const password = 'correct horse battery staple';

  const res = await fetch(`${AUTH_API_URL}/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      email,
      password,
      displayName: `Setup ${suffix}`,
      locale: 'en',
    }),
  });
  if (!res.ok) throw new Error(`POST /auth/register -> ${res.status} ${await res.text()}`);
  const body = (await res.json()) as { accessToken: string };
  return { email, password, accessToken: body.accessToken };
}

async function loginInContext(
  context: BrowserContext,
  email: string,
  password: string,
): Promise<void> {
  const page = await context.newPage();
  try {
    // AUTH_API_URL is a JSON API endpoint — `goto` may abort with
    // ERR_ABORTED (no HTML body / non-2xx) or time out before the
    // navigation "commit" event fires. The navigation itself is
    // throwaway: we only need the browser to adopt the auth-api
    // origin so the subsequent same-origin `fetch` inherits the
    // session cookies that auth-api sets via Set-Cookie. The follow-
    // up `page.evaluate` call below is the real assertion gate (it
    // throws on non-OK login), so swallowing the goto error here is
    // safe and intentional.
    await page.goto(AUTH_API_URL, { waitUntil: 'commit', timeout: 5000 }).catch(() => {});
    const r = await page.evaluate(
      async (creds: { email: string; password: string; authUrl: string }) => {
        const res = await fetch(`${creds.authUrl}/auth/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ email: creds.email, password: creds.password }),
        });
        return { ok: res.ok, status: res.status };
      },
      { email, password, authUrl: AUTH_API_URL },
    );
    if (!r.ok) throw new Error(`Browser-side login failed: ${r.status}`);
  } finally {
    await page.close();
  }
}

test.describe('setup first-run', () => {
  test('registers, fills the form, creates a workspace, and lands on /calendar', async ({
    page,
  }) => {
    const fresh = await registerWorkspaceless();
    await loginInContext(page.context(), fresh.email, fresh.password);

    await page.goto('/setup');

    // Form is visible because the user has no workspaces yet.
    await expect(page.getByRole('heading', { name: copy.title, level: 1 })).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByText(copy.description)).toBeVisible();

    const nameInput = page.getByPlaceholder(copy.namePlaceholder, { exact: true });
    const slugInput = page.getByPlaceholder(copy.slugPlaceholder, { exact: true });
    await expect(nameInput).toBeVisible();
    await expect(slugInput).toBeVisible();

    // Auto-slug: filling the name updates slug until the slug field is
    // explicitly edited. The slugify helper lowercases + dashes the name.
    const wsName = `Setup E2E ${Date.now().toString(36)}`;
    const expectedSlug = wsName
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '');
    await nameInput.fill(wsName);
    await expect(slugInput).toHaveValue(expectedSlug, { timeout: 2_000 });

    // Submit → backend creates the workspace and the SPA replaces /setup
    // with /calendar (it never re-renders the setup form post-success).
    await page.getByRole('button', { name: copy.submit, exact: true }).click();
    await expect(page).toHaveURL(/\/calendar$/, { timeout: 15_000 });
  });

  test('redirects users who already have a workspace away from /setup', async ({ page }) => {
    // The shared `user` tenant from globalSetup already has a workspace.
    // Visiting /setup must redirect to /calendar without rendering the form.
    const { loadTenants } = await import('./fixtures/load-tenants');
    const { injectAuth } = await import('./fixtures/tenant');
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);

    await page.goto('/setup');
    await expect(page).toHaveURL(/\/calendar$/, { timeout: 10_000 });
    await expect(page.getByRole('heading', { name: copy.title, level: 1 })).toHaveCount(0);
  });
});
