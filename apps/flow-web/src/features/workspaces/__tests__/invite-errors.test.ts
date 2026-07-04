import { ApiError } from '@nodate-flow/sdk';
import { describe, expect, it } from 'vitest';

import { resolveInviteErrorKey } from '../invite-errors';

describe('resolveInviteErrorKey', () => {
  it('maps expired and exhausted invite errors to specific copy', () => {
    expect(
      resolveInviteErrorKey(new ApiError('WS.WORKSPACE_INVITE.EXPIRED', 'invite expired')),
    ).toBe('workspaces.invites.expired');
    expect(
      resolveInviteErrorKey(new ApiError('WS.WORKSPACE_INVITE.EXHAUSTED', 'invite exhausted')),
    ).toBe('workspaces.invites.full');
  });

  it('keeps network failures distinct from invalid invite responses', () => {
    expect(resolveInviteErrorKey(new TypeError('failed to fetch'))).toBe(
      'workspaces.invites.error_network',
    );
    expect(resolveInviteErrorKey(new ApiError('WS.WORKSPACE_INVITE.NOT_FOUND', 'not found'))).toBe(
      'workspaces.invites.invalid',
    );
  });
});
