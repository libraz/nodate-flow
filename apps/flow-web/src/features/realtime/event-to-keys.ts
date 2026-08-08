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
  | 'label.changed'
  | 'reaction.changed'
  | 'favorite.changed'
  | 'intake.changed'
  | 'import.changed'
  | 'calendar.changed'
  | 'item.changed'
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
    case 'label.changed':
      return [['labels', 'list', ws], ['tasks']];
    case 'reaction.changed':
      return [['reactions']];
    case 'favorite.changed':
      return [['favorites', 'list', ws]];
    case 'intake.changed':
      // The intake list is cached per (workspace, status), so the
      // workspace prefix is what reaches every status tab. A longer
      // prefix would match nothing and leave the open tab stale.
      return [['intake', ws]];
    case 'import.changed':
      return [['imports', 'list', ws]];
    case 'calendar.changed':
      // calendar.* and share.* appends: public-shares, calendars,
      // events, members, memos. Invalidations are no-ops for pages
      // that don't subscribe.
      return [
        ['calendars', ws],
        ['events', ws],
        ['public-shares', ws],
      ];
    case 'item.changed':
      // item.* is itemkit's atomic task+event mutation. Because one
      // transaction touches both sides of the link, invalidate both
      // caches plus any derived views the reconciler emits to.
      return [['tasks'], ['calendars', ws], ['events', ws]];
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
        ['labels', 'list', ws],
        ['reactions'],
        ['favorites', 'list', ws],
        ['intake', ws],
        ['imports', 'list', ws],
        ['calendars', ws],
        ['events', ws],
        ['public-shares', ws],
        ['tasks'],
      ];
  }
}
