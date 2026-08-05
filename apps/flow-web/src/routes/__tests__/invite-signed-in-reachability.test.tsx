/**
 * `/invite/$token` for a visitor who is already signed in.
 *
 * The route sits outside `_authenticated`, so nothing there used to
 * re-establish the session from the refresh cookie. Invite links arrive
 * by mail or chat and open in a browser context with the cookie but an
 * empty in-memory store, which is precisely the case that used to render
 * "you need to sign in" to someone who already had. These tests pin the
 * two halves of the fix: the page waits for the session probe instead of
 * guessing, and the probe is skipped on public routes that never branch
 * on auth state.
 */

import { authStore } from '@nodate-flow/sdk';
import { render, screen } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AuthBootstrapStatus } from '../../features/auth/use-auth-bootstrap';

const mocks = vi.hoisted(() => ({
  bootstrapStatus: 'loading' as string,
  acceptInvite: vi.fn(),
}));

vi.mock('@tanstack/react-router', () => ({
  createLazyFileRoute:
    () =>
    (options: {
      component: () => ReactElement;
    }): { options: { component: () => ReactElement } } => ({
      options,
    }),
  getRouteApi: () => ({ useParams: () => ({ token: 'tok-1' }) }),
  useNavigate: () => vi.fn(),
  Link: ({ to, children }: { to: string; children: ReactNode }): ReactElement => (
    <a href={to}>{children}</a>
  ),
}));

vi.mock('../../features/auth/use-auth-bootstrap', () => ({
  useAuthBootstrap: () => ({ status: mocks.bootstrapStatus as AuthBootstrapStatus }),
}));

vi.mock('../../features/workspaces/invite-api', () => ({
  useInviteInfoQuery: () => ({ data: { workspaceName: 'Acme', role: 'member' } }),
  useAcceptInvite: () => ({ mutateAsync: mocks.acceptInvite }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { resolvedLanguage: 'en' } }),
}));

import { shouldProbeSession } from '../../lib/session-probe';
import { Route } from '../invite.$token.lazy';

function renderInvitePage(): void {
  // The router mock hands the options object straight back, so this is
  // the page component the real route would mount. TypeScript still
  // types it from the genuine LazyRoute signature, hence the cast.
  const Page = Route.options.component as () => ReactElement;
  render(<Page />);
}

function signIn(): void {
  authStore.getState().setSession('token-abc', {
    id: 'user-1',
    email: 'a@example.com',
    displayName: 'A',
    locale: 'en',
    timezone: 'UTC',
    country: 'JP',
    themePreference: 'system',
    isInstanceAdmin: false,
    avatarUrl: null,
  });
}

beforeEach(() => {
  mocks.bootstrapStatus = 'loading';
  mocks.acceptInvite.mockReset();
  authStore.getState().clearSession();
});

describe('/invite/$token when a session already exists', () => {
  it('offers the join action instead of asking the visitor to sign in', () => {
    signIn();
    mocks.bootstrapStatus = 'authenticated';
    renderInvitePage();

    expect(screen.getByText('workspaces.invites.join_button')).toBeDefined();
    expect(screen.queryByText('workspaces.invites.login_required')).toBeNull();
  });

  it('waits for the session probe rather than declaring the visitor signed out', () => {
    // Cold open from a mail client: the cookie is there, the store is
    // not, and the probe has not answered yet.
    mocks.bootstrapStatus = 'loading';
    renderInvitePage();

    expect(screen.queryByText('workspaces.invites.login_required')).toBeNull();
    expect(screen.getByRole('status')).toBeDefined();
  });

  it('points a genuinely signed-out visitor at login once the probe answers', () => {
    mocks.bootstrapStatus = 'unauthenticated';
    renderInvitePage();

    expect(screen.getByText('workspaces.invites.login_required')).toBeDefined();
    expect(screen.queryByText('workspaces.invites.join_button')).toBeNull();
  });
});

describe('shouldProbeSession', () => {
  it('probes routes whose rendering depends on the session', () => {
    expect(shouldProbeSession('/invite/tok-1')).toBe(true);
    expect(shouldProbeSession('/')).toBe(true);
    expect(shouldProbeSession('/calendar')).toBe(true);
  });

  it('leaves public views alone so a signed-out viewer spends no refresh budget', () => {
    // Each of these renders identically with or without a session, so a
    // probe there is a guaranteed-to-fail request against the caller's
    // rate limit on every single view.
    expect(shouldProbeSession('/share/cal/abc')).toBe(false);
    expect(shouldProbeSession('/embed/cal/abc')).toBe(false);
    expect(shouldProbeSession('/public/lenses/abc')).toBe(false);
    expect(shouldProbeSession('/invites/accept')).toBe(false);
    expect(shouldProbeSession('/login')).toBe(false);
    expect(shouldProbeSession('/signup')).toBe(false);
  });

  it('does not confuse the workspace-invite route with the calendar RSVP one', () => {
    // `/invite/$token` (workspace join) and `/invites/accept` (calendar
    // RSVP) differ by one character and want opposite treatment.
    expect(shouldProbeSession('/invite/tok-1')).toBe(true);
    expect(shouldProbeSession('/invites/accept')).toBe(false);
  });
});
