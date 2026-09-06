/**
 * Mentions feature — the people a body can name.
 *
 * The candidate list is the workspace member list, read through the same
 * query key the assignee pickers use so opening the picker on the task
 * page costs no request at all.
 */

import { type UseQueryResult, useQuery } from '@tanstack/react-query';

import type { ApiError } from '../../lib/api-error';
import { fetchWorkspaceMembers, type WorkspaceMember, workspacesKeys } from '../workspaces/api';

/** One person the picker can offer. */
export type MentionCandidate = WorkspaceMember;

/**
 * Workspace members, fetched without suspending and without escalating a
 * failure to an error boundary.
 *
 * Both departures from the app-wide query defaults are deliberate. The
 * picker opens inside a field someone is typing into: suspending would
 * unmount the editor mid-sentence, and `throwOnError` would tear down the
 * whole route and take the unsaved draft with it. A member list that
 * cannot be loaded is a picker that says so and a body that can still be
 * written by hand.
 */
export function useMentionCandidatesQuery(
  workspaceId: string | undefined,
  enabled: boolean,
): UseQueryResult<MentionCandidate[], ApiError> {
  return useQuery({
    queryKey: workspacesKeys.members(workspaceId ?? ''),
    queryFn: () => fetchWorkspaceMembers(workspaceId ?? ''),
    enabled: enabled && workspaceId !== undefined && workspaceId.length > 0,
    throwOnError: false,
  });
}

/**
 * Members whose display name or email contains `query`, case-insensitive.
 * An empty query offers everyone, so the picker shows the workspace the
 * moment `@` is typed rather than waiting for a letter.
 */
export function filterCandidates(
  candidates: readonly MentionCandidate[],
  query: string,
): MentionCandidate[] {
  if (query.length === 0) return [...candidates];
  const needle = query.toLowerCase();
  return candidates.filter(
    (candidate) =>
      candidate.displayName.toLowerCase().includes(needle) ||
      candidate.email.toLowerCase().includes(needle),
  );
}
