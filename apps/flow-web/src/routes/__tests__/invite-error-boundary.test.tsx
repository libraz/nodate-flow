/**
 * A failed invite lookup renders the branded error card rather than the
 * router's generic fallback, and the copy it shows names the reason:
 * expired, exhausted, offline, or otherwise invalid.
 *
 * The route's own `errorComponent` is rendered here with the error the
 * boundary would receive, so the mapping is observed through what the
 * user reads instead of through the text of the route file.
 */

import { renderWithProviders } from '@tests/helpers/render';
import type { ReactElement } from 'react';
import { describe, expect, it } from 'vitest';
import { resolveInviteErrorKey } from '../../features/workspaces/invite-errors';
import { ApiError } from '../../lib/api-error';
import { Route } from '../invite.$token';

const ErrorComponent = Route.options.errorComponent as (props: { error: unknown }) => ReactElement;

function apiError(code: string, status: number): ApiError {
  return new ApiError(code, code, status);
}

describe('invite error key resolution', () => {
  it('names an expired invite', () => {
    expect(resolveInviteErrorKey(apiError('WS.WORKSPACE_INVITE.EXPIRED', 410))).toBe(
      'workspaces.invites.expired',
    );
  });

  it('names an invite that ran out of seats', () => {
    expect(resolveInviteErrorKey(apiError('WS.WORKSPACE_INVITE.EXHAUSTED', 409))).toBe(
      'workspaces.invites.full',
    );
  });

  it('falls back to invalid for any other refusal', () => {
    expect(resolveInviteErrorKey(apiError('WS.WORKSPACE_INVITE.NOT_FOUND', 404))).toBe(
      'workspaces.invites.invalid',
    );
    expect(resolveInviteErrorKey(new Error('boom'))).toBe('workspaces.invites.invalid');
  });

  it('separates a transport failure from a refused invite', () => {
    expect(resolveInviteErrorKey(new TypeError('Failed to fetch'))).toBe(
      'workspaces.invites.error_network',
    );
  });
});

describe('invite error boundary rendering', () => {
  it('renders the branded card with the reason and a way back', () => {
    const { container, getByRole } = renderWithProviders(
      <ErrorComponent error={apiError('WS.WORKSPACE_INVITE.EXPIRED', 410)} />,
    );

    // The card renders at all: without this the "shows the right reason"
    // assertions below would also hold for an empty tree.
    expect(container.textContent).not.toBe('');
    expect(getByRole('heading', { level: 1 })).toBeDefined();
    expect(getByRole('link')).toBeDefined();
    expect(container.textContent).toContain('workspaces.invites.expired');
  });

  it('shows a different reason for an exhausted invite', () => {
    const { container } = renderWithProviders(
      <ErrorComponent error={apiError('WS.WORKSPACE_INVITE.EXHAUSTED', 409)} />,
    );
    expect(container.textContent).toContain('workspaces.invites.full');
    expect(container.textContent).not.toContain('workspaces.invites.expired');
  });
});
