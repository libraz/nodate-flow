/**
 * Calendar event shift hooks — propose / apply the umbrella event move
 * with optional task drag-along selection.
 *
 * API direction:
 *   An umbrella calendar event is the anchor; the actor picks which
 *   linked tasks travel along when the event itself shifts.
 *
 * Endpoints:
 *   - POST /workspaces/{wsId}/calendar-events/{evtId}/propose-shift
 *     {@link useProposeShiftMutation} — read-only preview, no cache writes.
 *   - POST /workspaces/{wsId}/calendar-events/{evtId}/apply-shift
 *     {@link useApplyShiftMutation} — invalidates the event detail cache
 *     plus the three calendar-grid caches so any open surface reflects
 *     the move in a single tick.
 */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '../../lib/api';
import type { ApiError } from '../../lib/api-error';
import { eventDetailKeys } from './api';

export type ShiftProposal = components['schemas']['ProposeShiftOutputBody'];
export type ShiftCandidate = components['schemas']['ShiftCandidateDTO'];
export type ShiftResult = components['schemas']['ApplyShiftOutputBody'];

export interface ProposeShiftArgs {
  wsId: string;
  evtId: string;
  newStartAt: number;
}

/**
 * POST /workspaces/{wsId}/calendar-events/{evtId}/propose-shift.
 *
 * Returns the safe / conflict task split alongside the delta so the
 * confirm dialog can render two checkbox lists. No cache invalidation:
 * propose is purely read-only.
 */
export function useProposeShiftMutation(): UseMutationResult<
  ShiftProposal,
  ApiError,
  ProposeShiftArgs
> {
  return useMutation<ShiftProposal, ApiError, ProposeShiftArgs>({
    mutationFn: async ({ wsId, evtId, newStartAt }): Promise<ShiftProposal> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendar-events/{evtId}/propose-shift', {
            params: { path: { wsId, evtId } },
            body: { newStartAt },
          }),
        'Failed to preview shift',
      );
      return data;
    },
  });
}

export interface ApplyShiftArgs {
  wsId: string;
  /**
   * Calendar id is required to invalidate the event-detail cache row,
   * which is keyed on `(wsId, calId, evtId)`.
   */
  calId: string;
  evtId: string;
  newStartAt: number;
  confirmedTaskIds: string[];
}

/**
 * POST /workspaces/{wsId}/calendar-events/{evtId}/apply-shift.
 *
 * On success invalidates four caches so every open surface refreshes:
 *   - `eventDetailKeys.detail(wsId, calId, evtId)` — header start/end.
 *   - `['calendar-events', 'list', wsId, calId]`  — calendar grid pills.
 *   - `['calendar', 'me-events']`                 — cross-workspace agenda.
 *   - `['calendar', 'me-tasks']`                  — task pills that moved.
 */
export function useApplyShiftMutation(): UseMutationResult<ShiftResult, ApiError, ApplyShiftArgs> {
  const qc = useQueryClient();
  return useMutation<ShiftResult, ApiError, ApplyShiftArgs>({
    mutationFn: async ({ wsId, evtId, newStartAt, confirmedTaskIds }): Promise<ShiftResult> => {
      const data = await apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/calendar-events/{evtId}/apply-shift', {
            params: { path: { wsId, evtId } },
            body: { newStartAt, confirmedTaskIds },
          }),
        'Failed to apply shift',
      );
      return data;
    },
    onSuccess: (_data, { wsId, calId, evtId }) => {
      void qc.invalidateQueries({ queryKey: eventDetailKeys.detail(wsId, calId, evtId) });
      void qc.invalidateQueries({ queryKey: ['calendar-events', 'list', wsId, calId] });
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-events'] });
      void qc.invalidateQueries({ queryKey: ['calendar', 'me-tasks'] });
    },
  });
}
