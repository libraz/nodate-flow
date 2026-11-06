/** API mutation for capturing an inbox signal as an intake item or linking it to a task. */

import type { components } from '@nodate-flow/sdk';
import { type UseMutationResult, useMutation, useQueryClient } from '@tanstack/react-query';

import { apiRequest } from '../../../lib/api';
import type { ApiError } from '../../../lib/api-error';
import { tasksKeys } from '../../tasks/api';
import { inboxKeys } from '../api';
import { intakeKeys } from '../intake/api';

export type CapturedIntake = components['schemas']['CaptureAsIntakeBody'];

export interface CaptureSourceArgs {
  workspaceId: string;
  signalId: string;
  existingTaskId?: string;
}

export function useCaptureSourceAsIntakeMutation(): UseMutationResult<
  CapturedIntake,
  ApiError,
  CaptureSourceArgs
> {
  const queryClient = useQueryClient();
  return useMutation<CapturedIntake, ApiError, CaptureSourceArgs>({
    mutationFn: async ({ workspaceId, signalId, existingTaskId }): Promise<CapturedIntake> => {
      return apiRequest(
        (client) =>
          client.POST('/workspaces/{wsId}/inbox/{signalId}/intake', {
            params: { path: { wsId: workspaceId, signalId } },
            body: existingTaskId ? { existingTaskId } : {},
          }),
        'Failed to capture inbox source',
      );
    },
    onSuccess: (_result, args) => {
      void queryClient.invalidateQueries({ queryKey: inboxKeys.list() });
      void queryClient.invalidateQueries({ queryKey: intakeKeys.all });
      if (args.existingTaskId) {
        void queryClient.invalidateQueries({ queryKey: tasksKeys.sources(args.existingTaskId) });
      }
    },
  });
}
