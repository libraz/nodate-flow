/**
 * The security settings page offers password change and two-factor
 * management only on an instance that has them, for an account that holds
 * a local password, and only once the capabilities are known.
 */

import { fireEvent, screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock('../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get },
  authSdk: { GET: sdkMocks.get },
}));

vi.mock('../../features/settings/password-panel', () => ({
  default: () => <p>password panel</p>,
}));
vi.mock('../../features/settings/totp-panel', () => ({
  default: () => <p>totp panel</p>,
}));
vi.mock('../../features/settings/sessions-panel', () => ({
  default: () => <p>sessions panel</p>,
}));

import { authStore } from '../../features/auth/auth-store';
import { Route } from '../_authenticated.settings.security.lazy';

const SecurityRoute = Route.options.component as () => ReactElement;

const CAPS = {
  passwordLogin: true,
  oidcGoogle: true,
  oidcGithub: false,
  oidcMicrosoft: false,
  magicLink: false,
  totp: true,
  registrationOpen: true,
};

function ok(overrides: Partial<typeof CAPS> = {}): Promise<unknown> {
  return Promise.resolve({
    data: { ...CAPS, ...overrides },
    error: null,
    response: new Response(null, { status: 200 }),
  });
}

function setUser(hasPassword: boolean): void {
  authStore.getState().setSession('tok', {
    id: 'u1',
    email: 'a@b.test',
    displayName: 'Alice',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    hasPassword,
  });
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  setUser(true);
});

afterEach(() => {
  authStore.getState().clearSession();
});

describe('settings security page and the capabilities', () => {
  it('shows neither section while the capabilities are pending', async () => {
    sdkMocks.get.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<SecurityRoute />);
    await screen.findByText('sessions panel');
    expect(screen.queryByText('password panel')).toBeNull();
    expect(screen.queryByText('totp panel')).toBeNull();
  });

  it('offers a retry when the capabilities failed to load', async () => {
    sdkMocks.get
      .mockResolvedValueOnce({
        data: null,
        error: { detail: 'boom' },
        response: new Response(null, { status: 500 }),
      })
      .mockReturnValue(ok());
    renderWithProviders(<SecurityRoute />);
    fireEvent.click(await screen.findByRole('button', { name: 'security.retry' }));
    expect(await screen.findByText('password panel')).toBeDefined();
    expect(screen.queryByRole('button', { name: 'security.retry' })).toBeNull();
  });

  it('shows password change and two-factor management when both are on', async () => {
    sdkMocks.get.mockReturnValue(ok());
    renderWithProviders(<SecurityRoute />);
    expect(await screen.findByText('password panel')).toBeDefined();
    expect(screen.getByText('totp panel')).toBeDefined();
  });

  it('hides both when password login is disabled', async () => {
    sdkMocks.get.mockReturnValue(ok({ passwordLogin: false, totp: false }));
    renderWithProviders(<SecurityRoute />);
    await screen.findByText('sessions panel');
    await vi.waitFor(() => {
      expect(sdkMocks.get).toHaveBeenCalled();
    });
    expect(screen.queryByText('password panel')).toBeNull();
    expect(screen.queryByText('totp panel')).toBeNull();
    expect(screen.queryByText('security.password.title')).toBeNull();
  });

  it('hides two-factor management when the server does not offer it', async () => {
    sdkMocks.get.mockReturnValue(ok({ totp: false }));
    renderWithProviders(<SecurityRoute />);
    expect(await screen.findByText('password panel')).toBeDefined();
    expect(screen.queryByText('totp panel')).toBeNull();
  });

  it('hides both from an account without a local password', async () => {
    setUser(false);
    sdkMocks.get.mockReturnValue(ok());
    renderWithProviders(<SecurityRoute />);
    await screen.findByText('sessions panel');
    await vi.waitFor(() => {
      expect(sdkMocks.get).toHaveBeenCalled();
    });
    expect(screen.queryByText('password panel')).toBeNull();
    expect(screen.queryByText('totp panel')).toBeNull();
  });
});
