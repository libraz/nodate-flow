/**
 * useUnlinkEvent — DELETE `/tasks/{id}/links/{linkId}` with optimistic
 * row removal.
 *
 * The row is removed from the cache before the network round-trip so
 * the shake-out animation lines up with the click. Failure rolls the
 * cache back and surfaces the `unlinkFailed` toast at the call site.
 */

import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '../../../../lib/api';
import type { ApiError } from '../../../../lib/api-error';
import { type LinkedEventsResult, linkedEventsKeys } from './use-linked-events';

export interface UnlinkEventArgs {
  taskId: string;
  linkId: string;
}

export function useUnlinkEvent(): UseMutationResult<
  void,
  ApiError,
  UnlinkEventArgs,
  { previous: LinkedEventsResult | undefined }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, linkId }: UnlinkEventArgs): Promise<void> => {
      await apiRequest(
        (client) =>
          client.DELETE('/tasks/{id}/links/{linkId}', {
            params: { path: { id: taskId, linkId } },
          }),
        'Failed to unlink event',
      );
    },
    onMutate: async ({ taskId, linkId }) => {
      const key = linkedEventsKeys.list(taskId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<LinkedEventsResult>(key);
      if (previous) {
        const links = previous.links.filter((l) => l.id !== linkId);
        qc.setQueryData<LinkedEventsResult>(key, {
          links,
          total: Math.max(0, previous.total - 1),
        });
      }
      return { previous };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous !== undefined) {
        qc.setQueryData(linkedEventsKeys.list(vars.taskId), ctx.previous);
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: linkedEventsKeys.list(vars.taskId) });
    },
  });
}
