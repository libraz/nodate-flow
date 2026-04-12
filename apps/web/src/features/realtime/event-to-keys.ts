/**
 * event-to-keys — pure mapping from wire-format stream events to the
 * TanStack Query key prefixes they should invalidate.
 *
 * Single source of truth for "what does each SSE event kind mean to
 * the UI layer". Keeping the mapping here (and not inside
 * `use-workspace-stream.ts`) makes it trivial to unit test without a
 * running fetch reader.
 *
 * Keys are returned as **prefixes**: the caller passes each one to
 * `queryClient.invalidateQueries({ queryKey })` and TanStack Query's
 * prefix-match semantics take care of the rest.
 */

export type StreamKind =
  | 'task.changed'
  | 'ai.suggestion.changed'
  | 'ai.invocation.written'
  | 'notification.changed'
  | 'timebox.changed'
  | 'relation.changed'
  | 'lens.changed'
  | 'page.changed'
  | 'dashboard.changed'
  | 'resync';

export interface StreamEvent {
  kind: StreamKind;
  workspaceId: string;
  at: number;
}

/**
 * keysForEvent returns the list of queryKey prefixes that should be
 * invalidated in response to the given stream event. The returned
 * keys are ordered from most specific to least specific so the caller
 * can stop early if desired.
 */
export function keysForEvent(evt: StreamEvent): readonly (readonly unknown[])[] {
  const ws = evt.workspaceId;
  switch (evt.kind) {
    case 'task.changed':
      return [
        ['auto-actions', 'list', ws],
        ['reminders', 'list', ws],
        ['state-suggestions', 'list', ws],
        ['weekly-digest', 'workspace', ws],
        // Task lists + task detail caches don't namespace by workspace
        // today, so fall back to the root prefix.
        ['tasks'],
      ];
    case 'ai.suggestion.changed':
      return [['ai-suggestions', 'list', ws]];
    case 'ai.invocation.written':
      return [['ai-invocations', 'list', ws]];
    case 'notification.changed':
      return [
        ['notifications', 'list'],
        ['notifications', 'unread-count'],
      ];
    case 'timebox.changed':
      return [
        ['timeboxes', 'list', ws],
        ['timeboxes', 'detail'],
      ];
    case 'relation.changed':
      return [
        ['relation-suggestions', 'list', ws],
        ['relation-suggestions', 'task'],
      ];
    case 'lens.changed':
      return [['lenses', 'list', ws]];
    case 'page.changed':
      return [['pages', 'list', ws]];
    case 'dashboard.changed':
      return [['dashboard', 'list', ws]];
    case 'resync':
      return [
        ['auto-actions', 'list', ws],
        ['reminders', 'list', ws],
        ['state-suggestions', 'list', ws],
        ['weekly-digest', 'workspace', ws],
        ['ai-suggestions', 'list', ws],
        ['ai-invocations', 'list', ws],
        ['notifications', 'list'],
        ['notifications', 'unread-count'],
        ['timeboxes', 'list', ws],
        ['timeboxes', 'detail'],
        ['relation-suggestions', 'list', ws],
        ['relation-suggestions', 'task'],
        ['lenses', 'list', ws],
        ['pages', 'list', ws],
        ['dashboard', 'list', ws],
        ['tasks'],
      ];
  }
}
