import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseQueryResult,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { apiRequest } from '../../../lib/api';
import { tasksKeys } from '../api';

export type WorkflowSnapshot = components['schemas']['WorkflowSnapshot'];
export type WorkflowSubmission = components['schemas']['WorkflowSubmission'];
export type WorkflowHandoff = components['schemas']['WorkflowHandoff'];
export type CompletionPolicyBody = components['schemas']['CompletionPolicyBody'];
export type CreateSubmissionBody = components['schemas']['CreateSubmissionBody'];
export type ReviewDecisionBody = components['schemas']['ReviewDecisionBody'];
export type CreateHandoffBody = components['schemas']['CreateHandoffBody'];
export type HandoffDecisionBody = components['schemas']['HandoffDecisionBody'];

export const workflowKeys = {
  all: tasksKeys.all,
  detail: (taskId: string) => tasksKeys.workflow(taskId),
};

export function useWorkflowQuery(taskId: string): UseQueryResult<WorkflowSnapshot> {
  return useQuery({
    queryKey: workflowKeys.detail(taskId),
    queryFn: async (): Promise<WorkflowSnapshot> =>
      apiRequest(
        (client) =>
          client.GET('/tasks/{id}/workflow', {
            params: { path: { id: taskId } },
          }),
        'Failed to load task workflow',
      ),
  });
}

function invalidateWorkflow(qc: ReturnType<typeof useQueryClient>, taskId: string): void {
  void qc.invalidateQueries({ queryKey: workflowKeys.detail(taskId) });
  void qc.invalidateQueries({ queryKey: tasksKeys.detail(taskId), exact: true });
}

export interface SetCompletionPolicyArgs {
  taskId: string;
  input: CompletionPolicyBody;
}

export function useSetCompletionPolicy(): UseMutationResult<
  components['schemas']['CompletionPolicy'],
  Error,
  SetCompletionPolicyArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/completion-policy', {
            params: { path: { id: taskId } },
            body: input,
          }),
        'Failed to set task completion policy',
      ),
    onSuccess: (_data, { taskId }) => invalidateWorkflow(qc, taskId),
  });
}

export interface CreateSubmissionArgs {
  taskId: string;
  input: CreateSubmissionBody;
}

export function useCreateSubmission(): UseMutationResult<
  components['schemas']['SubmissionBody'],
  Error,
  CreateSubmissionArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/submissions', {
            params: { path: { id: taskId } },
            body: input,
          }),
        'Failed to submit task work for review',
      ),
    onSuccess: (_data, { taskId }) => invalidateWorkflow(qc, taskId),
  });
}

export interface DecideSubmissionArgs {
  taskId: string;
  submissionId: string;
  input: ReviewDecisionBody;
}

export function useDecideSubmission(): UseMutationResult<
  components['schemas']['DecisionBody'],
  Error,
  DecideSubmissionArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, submissionId, input }) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/submissions/{submissionId}/decision', {
            params: { path: { id: taskId, submissionId } },
            body: input,
          }),
        'Failed to record the review decision',
      ),
    onSuccess: (_data, { taskId }) => invalidateWorkflow(qc, taskId),
  });
}

export interface CreateHandoffArgs {
  taskId: string;
  input: CreateHandoffBody;
}

export function useCreateHandoff(): UseMutationResult<
  components['schemas']['HandoffBody'],
  Error,
  CreateHandoffArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, input }) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/handoffs', {
            params: { path: { id: taskId } },
            body: input,
          }),
        'Failed to request task handoff',
      ),
    onSuccess: (_data, { taskId }) => {
      invalidateWorkflow(qc, taskId);
    },
  });
}

export interface DecideHandoffArgs {
  taskId: string;
  handoffId: string;
  input: HandoffDecisionBody;
}

export function useDecideHandoff(): UseMutationResult<
  components['schemas']['HandoffBody'],
  Error,
  DecideHandoffArgs
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ taskId, handoffId, input }) =>
      apiRequest(
        (client) =>
          client.POST('/tasks/{id}/handoffs/{handoffId}/decision', {
            params: { path: { id: taskId, handoffId } },
            body: input,
          }),
        'Failed to decide task handoff',
      ),
    onSuccess: (_data, { taskId }) => {
      void qc.invalidateQueries({ queryKey: tasksKeys.actors(taskId) });
      invalidateWorkflow(qc, taskId);
    },
  });
}
