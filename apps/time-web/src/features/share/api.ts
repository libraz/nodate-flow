/**
 * share/api — hooks for the public share route.
 *
 * The internal calendar UX lives in flow-web now (R5.6). time-web keeps
 * only the public share flow and the invite-accept handoff it triggers
 * after login. This file replaces the narrow slice of the former
 * `features/calendar/api.ts` that the share page still needs.
 */

import { useMutation } from '@tanstack/react-query';

import { toApiError } from '../../lib/api-error';
import { sdk } from '../../lib/sdk';

interface AcceptInviteResponse {
  calendarId: string;
  calendarName: string;
  role: string;
}

/** Accept a calendar invite by its one-time token. */
export function useAcceptInviteMutation() {
  return useMutation({
    mutationFn: async (token: string): Promise<AcceptInviteResponse> => {
      const result = await sdk.POST('/invites/{token}/accept', {
        params: { path: { token } },
        body: {},
      });
      if (result.error || !result.data) {
        throw toApiError(result.error, 'Failed to accept invite');
      }
      return result.data as AcceptInviteResponse;
    },
  });
}
