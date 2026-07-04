import { ApiError, isNetworkError } from '../../lib/api-error';

export function resolveInviteErrorKey(error: unknown): string {
  if (isNetworkError(error)) return 'workspaces.invites.error_network';
  if (error instanceof ApiError) {
    switch (error.code) {
      case 'WS.WORKSPACE_INVITE.EXPIRED':
        return 'workspaces.invites.expired';
      case 'WS.WORKSPACE_INVITE.EXHAUSTED':
        return 'workspaces.invites.full';
      default:
        return 'workspaces.invites.invalid';
    }
  }
  return 'workspaces.invites.invalid';
}
