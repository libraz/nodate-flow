/**
 * Discord personal-integration connect / disconnect flow E2E.
 *
 * Covers the panel wiring added in Phase 7 / P1 of release-8: the new
 * Discord row in apps/flow-web/src/features/settings/integrations-panel.tsx
 * must surface alongside github / slack / google_calendar, accept a
 * Connect click that drives the browser to a discord.com authorize URL,
 * and offer a working Disconnect button when a row is present.
 *
 * --------------------------------------------------------------------------
 * IMPORTANT — scope limitation
 * --------------------------------------------------------------------------
 * The full OAuth2 round-trip (state → authorize redirect → callback →
 * token exchange → /users/@me lookup) involves server-side calls from
 * auth-api to discord.com that Playwright cannot intercept (only the
 * browser-side requests are routable). In addition, the dev test
 * environment has no Discord OAuth credentials configured
 * (NF_AUTH_DISCORD_CLIENT_ID/SECRET/REDIRECT_URI are blank in .env), so
 * the real auth-api reports `configured: false` for Discord and the
 * Connect button is hidden.
 *
 * To exercise the UI wiring we therefore intercept GET /me/integrations
 * (and DELETE /me/integrations/{id}) at the browser layer and force the
 * shape the panel would see if the server had Discord configured. The
 * actual REST handlers + Discord provider are unit-tested separately by
 * @api in apps/auth-api/internal/integrations/discord_test.go and
 * apps/auth-api/internal/http/handlers/integrations/handlers_test.go.
 *
 * Cases:
 *   A. Login + Discord row appears in the integrations panel with the
 *      localised provider name + description.
 *   B. Connect flow — clicking Connect issues
 *      POST /me/integrations/discord/connect and the browser navigates
 *      to the returned authorize URL (discord.com/oauth2/authorize?...).
 *      We intercept that navigation so the spec does not actually leave
 *      localhost, and assert the URL shape.
 *   C. Disconnect flow — a stubbed connected Discord row surfaces a
 *      Disconnect button; clicking it opens the themed confirm dialog,
 *      confirming fires DELETE /me/integrations/{id}, and the panel
 *      then reflects the disconnected state again.
 *   D. Console clean — no console.error / console.warn for the lifetime
 *      of the spec.
 */

import { type ConsoleMessage, type Route, expect, test } from '@playwright/test';

import enSettings from '../../locales/en/settings.json' with { type: 'json' };
import { loadTenants } from '../fixtures/load-tenants';
import { AUTH_API_URL, injectAuth } from '../fixtures/tenant';

const integrationsCopy = enSettings.integrations;

const copy = {
  panelDescription: integrationsCopy.description,
  connect: integrationsCopy.connect,
  disconnect: integrationsCopy.disconnect,
  disconnected: integrationsCopy.disconnected,
  notConfigured: integrationsCopy.not_configured,
  discordName: integrationsCopy.provider.discord.name,
  discordDescription: integrationsCopy.provider.discord.description,
} as const;

/**
 * Shape of GET /me/integrations matching ProviderStatus[] in auth-api.
 * Kept in this file rather than imported from @nodate-flow/sdk because
 * the spec drives the stub responses by literal JSON.
 */
interface ConnectionSummary {
  id: string;
  provider: string;
  externalAccountId: string;
  externalAccountLabel: string;
  scopes: string;
  connectedAt: number;
  lastRefreshedAt?: number;
  accessTokenExpiresAt?: number;
}

interface ProviderStatus {
  provider: string;
  configured: boolean;
  connection?: ConnectionSummary;
}

interface IntegrationsListResponse {
  providers: ProviderStatus[];
}

const DISCORD_CONNECTION_ID = '01963000-7777-7000-8000-aaaaaaaaaaaa';
const DISCORD_SNOWFLAKE = '123456789012345678';
const DISCORD_LABEL = 'Test User';

/**
 * Builds the response the panel would see if all four providers were
 * configured. The flag for Discord toggles whether the panel renders
 * a Connect button or an inline "not configured" hint.
 */
function buildIntegrationsList(opts: {
  discordConfigured: boolean;
  discordConnected: boolean;
}): IntegrationsListResponse {
  const providers: ProviderStatus[] = [
    { provider: 'github', configured: false },
    { provider: 'slack', configured: false },
    { provider: 'google_calendar', configured: false },
    {
      provider: 'discord',
      configured: opts.discordConfigured,
    },
  ];
  if (opts.discordConnected) {
    const discordEntry = providers[3];
    if (discordEntry) {
      discordEntry.connection = {
        id: DISCORD_CONNECTION_ID,
        provider: 'discord',
        externalAccountId: DISCORD_SNOWFLAKE,
        externalAccountLabel: DISCORD_LABEL,
        scopes: 'identify guilds',
        connectedAt: 1_700_000_000,
      };
    }
  }
  return { providers };
}

/**
 * Console messages that are expected in the Playwright dev environment
 * and are not regressions caused by this panel. Currently:
 *   - VITE_PUBLIC_BASE_URL fallback warning: only emitted when
 *     getPublicBaseUrl() runs in dev where the build-time env is not
 *     set; the integrations panel hits this path when the user clicks
 *     Connect, but it is not a bug in the Discord flow itself.
 *
 * Filtered by substring match (no regex per project rule).
 */
const EXPECTED_CONSOLE_SUBSTRINGS: readonly string[] = ['VITE_PUBLIC_BASE_URL is not set'];

/**
 * Attaches a recorder that captures every browser console error or
 * warning emitted during the spec, minus the known-benign messages
 * above. Returns the array so the test can assert it stayed empty.
 */
function recordConsoleNoise(page: import('@playwright/test').Page): string[] {
  const noise: string[] = [];
  page.on('console', (msg: ConsoleMessage) => {
    if (msg.type() !== 'error' && msg.type() !== 'warning') return;
    const text = msg.text();
    for (const allow of EXPECTED_CONSOLE_SUBSTRINGS) {
      if (text.includes(allow)) return;
    }
    noise.push(`[${msg.type()}] ${text}`);
  });
  return noise;
}

test.describe('integrations panel — Discord', () => {
  test('A: Discord row renders with localised name + description', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    const noise = recordConsoleNoise(page);

    // Real GET /me/integrations returns discord with configured=false
    // because the dev auth-api has no NF_AUTH_DISCORD_* env set. Force
    // configured=false explicitly and assert the row + "not configured"
    // hint are surfaced (no Connect button enabled).
    await page.route(`${AUTH_API_URL}/me/integrations`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          buildIntegrationsList({ discordConfigured: false, discordConnected: false }),
        ),
      });
    });

    await page.goto('/settings/integrations');
    await page.waitForLoadState('domcontentloaded');

    // Panel description renders (sanity that the route loaded the panel).
    await expect(page.getByText(copy.panelDescription)).toBeVisible({ timeout: 10_000 });

    // Discord row surfaces both name and description.
    await expect(page.getByText(copy.discordName, { exact: true })).toBeVisible();
    await expect(page.getByText(copy.discordDescription, { exact: true })).toBeVisible();

    // The Discord row's "not configured" hint is visible because the
    // stub returned configured=false. The Connect button is rendered
    // for the discord row but disabled.
    const discordRow = page
      .getByRole('listitem')
      .filter({ has: page.getByText(copy.discordName, { exact: true }) });
    await expect(discordRow.getByText(copy.notConfigured)).toBeVisible();
    await expect(
      discordRow.getByRole('button', { name: copy.connect, exact: true }),
    ).toBeDisabled();

    expect(noise, `console noise during render: ${noise.join('\n')}`).toEqual([]);
  });

  test('B: Connect flow navigates to discord.com authorize URL', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    const noise = recordConsoleNoise(page);

    // Stub the panel feed so Discord renders as configured + ready to
    // connect. Without this override the dev server reports configured=
    // false and the button stays disabled.
    await page.route(`${AUTH_API_URL}/me/integrations`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          buildIntegrationsList({ discordConfigured: true, discordConnected: false }),
        ),
      });
    });

    // Stub the connect mutation. The real handler would persist an
    // oauth_states row and return a discord.com URL; the stub returns
    // the same shape with a synthetic state so the spec can assert the
    // browser navigates to the authorize endpoint.
    const fakeState = 'spec-state-abcdef0123456789';
    const authorizeUrl = `https://discord.com/oauth2/authorize?client_id=spec-client-id&redirect_uri=${encodeURIComponent('http://localhost:8082/oauth/callback/discord')}&state=${fakeState}&response_type=code&prompt=consent&scope=identify+guilds`;
    let connectCalled = false;
    await page.route(`${AUTH_API_URL}/me/integrations/discord/connect`, async (route) => {
      connectCalled = true;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authorizeUrl }),
      });
    });

    // The panel calls window.location.assign(authorizeUrl) which
    // triggers a top-level navigation. Intercept discord.com so the
    // spec does not actually hit the real site; respond with a tiny
    // stub page we can detect to know the navigation completed.
    await page.route('https://discord.com/oauth2/authorize**', async (route: Route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: '<!doctype html><html><body><h1>discord oauth stub</h1></body></html>',
      });
    });

    await page.goto('/settings/integrations');
    await page.waitForLoadState('domcontentloaded');

    const discordRow = page
      .getByRole('listitem')
      .filter({ has: page.getByText(copy.discordName, { exact: true }) });
    const connectButton = discordRow.getByRole('button', { name: copy.connect, exact: true });
    await expect(connectButton).toBeEnabled({ timeout: 10_000 });

    // Click + wait for the navigation to the discord.com stub.
    await Promise.all([page.waitForURL(/discord\.com\/oauth2\/authorize/), connectButton.click()]);

    expect(connectCalled, 'POST /me/integrations/discord/connect must have been called').toBe(true);

    // The browser ended up on the discord.com stub page. Assert the
    // resolved URL has every parameter the panel + server contract
    // need: state, response_type=code, scope=identify guilds, the
    // configured redirect_uri.
    const resolved = new URL(page.url());
    expect(resolved.host).toBe('discord.com');
    expect(resolved.pathname).toBe('/oauth2/authorize');
    expect(resolved.searchParams.get('state')).toBe(fakeState);
    expect(resolved.searchParams.get('response_type')).toBe('code');
    expect(resolved.searchParams.get('scope')).toBe('identify guilds');
    expect(resolved.searchParams.get('client_id')).toBe('spec-client-id');
    expect(resolved.searchParams.get('redirect_uri')).toBe(
      'http://localhost:8082/oauth/callback/discord',
    );

    expect(noise, `console noise during connect: ${noise.join('\n')}`).toEqual([]);
  });

  test('C: Disconnect flow confirms then removes the Discord row', async ({ page }) => {
    const { user: tenant } = loadTenants();
    await injectAuth(page.context(), tenant);
    const noise = recordConsoleNoise(page);

    // The panel calls GET /me/integrations on render AND after a
    // successful DELETE (the mutation invalidates the query). Toggle
    // the response based on a local flag.
    let discordConnected = true;
    await page.route(`${AUTH_API_URL}/me/integrations`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(buildIntegrationsList({ discordConfigured: true, discordConnected })),
      });
    });

    // Stub the DELETE handler. The real auth-api would best-effort
    // revoke at discord.com/api/oauth2/token/revoke first (server-side
    // call we cannot intercept here) and then delete the row. The
    // stub jumps straight to ok=true and flips the local flag so the
    // next GET reports the row as gone.
    let disconnectCalled = false;
    await page.route(
      `${AUTH_API_URL}/me/integrations/${DISCORD_CONNECTION_ID}`,
      async (route: Route) => {
        if (route.request().method() !== 'DELETE') {
          return route.fallback();
        }
        disconnectCalled = true;
        discordConnected = false;
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ ok: true }),
        });
      },
    );

    await page.goto('/settings/integrations');
    await page.waitForLoadState('domcontentloaded');

    const discordRow = page
      .getByRole('listitem')
      .filter({ has: page.getByText(copy.discordName, { exact: true }) });
    const disconnectButton = discordRow.getByRole('button', {
      name: copy.disconnect,
      exact: true,
    });
    await expect(disconnectButton).toBeVisible({ timeout: 10_000 });
    await disconnectButton.click();

    // Themed confirm dialog opens. The integrations panel passes the
    // localised Discord label into the disconnect_confirm message;
    // accept by clicking the affirmative button.
    const confirmDialog = page.getByRole('dialog');
    await expect(confirmDialog).toBeVisible({ timeout: 5_000 });
    // The dialog body interpolates the provider name. Assert the
    // interpolation actually happened so a future broken i18n key
    // does not silently strip the placeholder.
    await expect(confirmDialog).toContainText(copy.discordName);
    // The themed confirm registers locale-aware fallbacks via
    // setConfirmActionLabels in providers/i18n-provider; the EN
    // affirmative defaults to "Confirm" (or "実行" for ja). Match
    // both case-insensitively so a future locale flip on the shared
    // tenant does not break this spec.
    await confirmDialog.getByRole('button', { name: /^(confirm|delete|実行|削除)$/i }).click();

    expect(disconnectCalled, 'DELETE /me/integrations/{id} must have been called').toBe(true);

    // After the mutation onSuccess invalidates the list query, the
    // panel refetches and the row should now render Connect (not
    // Disconnect). The disconnected toast also appears, but we assert
    // on the structural Connect button presence because toaster
    // messages auto-dismiss and are harder to time without flake.
    const connectButton = discordRow.getByRole('button', { name: copy.connect, exact: true });
    await expect(connectButton).toBeVisible({ timeout: 10_000 });
    await expect(disconnectButton).toHaveCount(0);

    expect(noise, `console noise during disconnect: ${noise.join('\n')}`).toEqual([]);
  });
});
