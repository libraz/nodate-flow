/**
 * EventFilterBar — kind multi-select + workspace-scoped actor picker + reset.
 *
 * The actor picker is rendered only when a workspaceId is supplied; project
 * timelines that don't yet thread their parent workspace fall back to the
 * disabled placeholder used during F0.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import { ToggleChip, ToggleChipGroup } from '@nodate-flow/ui/primitives/toggle-chip';
import { ChevronDown } from 'lucide-react';
import { type ReactElement, Suspense, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceUsersQuery } from '../workspaces/api';
import type { TimelineFilters } from './api';
import styles from './event-filter-bar.module.css';

/**
 * One filter chip. `kinds` lists every event kind the chip selects — more
 * than one where a single user-facing action is encoded under both a
 * current and a historical kind, which a reader must not have to know
 * about. `key` is the kind whose `event_kind.*` label the chip wears.
 */
interface KindChip {
  key: string;
  kinds: readonly string[];
}

/** Build a chip for one kind, optionally selecting older encodings of
 * the same action alongside it. */
function chip(key: string, ...alsoSelects: readonly string[]): KindChip {
  return { key, kinds: [key, ...alsoSelects] };
}

/**
 * Grouped chips — drives the sectioned chip filter UI.
 *
 * Every kind listed here has a producer in the backend event vocabulary.
 * A chip that can never match would be a filter promising rows it cannot
 * return, so kinds the API has no way to emit are deliberately absent
 * even where this feature carries a label for them.
 */
export const KIND_GROUPS: readonly { key: string; chips: readonly KindChip[] }[] = [
  { key: 'task', chips: [chip('task.created'), chip('task.updated'), chip('task.disabled')] },
  {
    // `comment.added` is the historical encoding of `task.comment.added`
    // and still round-trips on old rows, so it rides the same chip
    // rather than sitting beside it under a second identical label.
    key: 'comment',
    chips: [
      chip('task.comment.added', 'comment.added'),
      chip('task.comment.edited'),
      chip('task.comment.removed'),
    ],
  },
  {
    key: 'attachment',
    chips: [chip('task.attachment.added'), chip('task.attachment.removed')],
  },
  { key: 'member', chips: [chip('task.actor.added'), chip('task.actor.removed')] },
  {
    key: 'dependency',
    chips: [chip('task.dependency.added'), chip('task.dependency.removed')],
  },
  {
    key: 'constraint',
    chips: [
      chip('task.constraint.added'),
      chip('task.constraint.removed'),
      chip('task.constraint.satisfied'),
      chip('task.constraint.failed'),
    ],
  },
  {
    key: 'transition',
    chips: [
      chip('task.transition.start'),
      chip('task.transition.block'),
      chip('task.transition.unblock'),
      chip('task.transition.submit'),
      chip('task.transition.complete'),
      chip('task.transition.reopen'),
      chip('task.transition.cancel'),
    ],
  },
  {
    key: 'review',
    chips: [chip('task.submission.created'), chip('task.review.decided')],
  },
  {
    key: 'handoff',
    chips: [chip('task.handoff.requested'), chip('task.handoff.decided')],
  },
  // A mention is its own group rather than a chip under `comment`: the
  // backend emits it from a description edit as well as from a comment,
  // so folding it into `comment` would make the chip claim a scope it
  // does not have. Like `signal`, a one-chip group is enough — the
  // groups are cut by what a reader wants to isolate, not by count.
  { key: 'mention', chips: [chip('mention.created')] },
  {
    key: 'page',
    chips: [
      chip('page.created'),
      chip('page.updated'),
      chip('page.disabled'),
      chip('page.archived'),
      chip('page.unarchived'),
    ],
  },
  {
    // Workspace membership, kept apart from the `member` group above:
    // that one is who is on a task, this one is who is in the workspace.
    key: 'workspace',
    chips: [
      chip('workspace.member.added'),
      chip('workspace.member.removed'),
      chip('workspace.member.role_changed'),
    ],
  },
  {
    // The calendar itself: created, renamed, removed, and the
    // subscription rows that record how one reader chose to see it.
    // `calendar.reminder` stays in this group — the scheduler appends
    // it when an occurrence comes due, and it was here before the rest
    // of the family arrived.
    key: 'calendar',
    chips: [
      chip('calendar.created'),
      chip('calendar.updated'),
      chip('calendar.deleted'),
      chip('calendar.subscribed'),
      chip('calendar.subscription.updated'),
      chip('calendar.label.created'),
      chip('calendar.label.updated'),
      chip('calendar.label.deleted'),
      chip('calendar.reminder'),
    ],
  },
  {
    // Who is on a calendar. A third membership group beside `member`
    // (who is on a task) and `workspace` (who is in the workspace),
    // for the same reason those two are apart: the three answer
    // different questions about different subjects.
    key: 'calendar_member',
    chips: [
      chip('calendar.member.added'),
      chip('calendar.member.removed'),
      chip('calendar.member.role_changed'),
      chip('calendar.member.updated'),
    ],
  },
  {
    // The events themselves, kept clear of the details that hang off
    // them so "what got scheduled or moved?" stays a one-group answer.
    key: 'calendar_event',
    chips: [
      chip('calendar.event.created'),
      chip('calendar.event.updated'),
      chip('calendar.event.deleted'),
    ],
  },
  {
    // What was written onto an event. These share the calendar.event.*
    // namespace with the lifecycle group above but are content edits,
    // and they outnumber the lifecycle kinds badly enough that folding
    // them together would hide the create/update/delete rows.
    key: 'calendar_event_content',
    chips: [
      chip('calendar.event.comment.created'),
      chip('calendar.event.comment.updated'),
      chip('calendar.event.comment.deleted'),
      chip('calendar.event.checklist.created'),
      chip('calendar.event.checklist.updated'),
      chip('calendar.event.checklist.deleted'),
      chip('calendar.event.attachment.created'),
      chip('calendar.event.attachment.deleted'),
    ],
  },
  {
    // Who is coming, and how they were asked. Invites sit with the
    // attendees rather than with the share group: an invite link admits
    // someone to one event, it does not publish anything.
    key: 'calendar_attendee',
    chips: [
      chip('calendar.event.attendee.added'),
      chip('calendar.event.attendee.removed'),
      chip('calendar.event.rsvp.updated'),
      chip('calendar.event.invite.created'),
      chip('calendar.event.invite.rotated'),
      chip('calendar.event.invite.revoked'),
    ],
  },
  {
    // `calendar.memo.completed` is declared and carries a label, but no
    // handler emits it, so it gets no chip under the rule above.
    key: 'calendar_memo',
    chips: [
      chip('calendar.memo.created'),
      chip('calendar.memo.updated'),
      chip('calendar.memo.deleted'),
    ],
  },
  {
    // Its own group rather than a section of `calendar`: every kind
    // here changes what is readable outside the workspace, which is the
    // question someone auditing the feed opens the filter to ask.
    key: 'public_share',
    chips: [
      chip('public_share.created'),
      chip('public_share.updated'),
      chip('public_share.rotated'),
      chip('public_share.deleted'),
      chip('public_share.events_attached'),
      chip('public_share.events_reordered'),
      chip('public_share.event_detached'),
    ],
  },
  {
    // The AI vocabulary splits three ways by what a reader is asking.
    // This group answers "what did the AI want to change?" — proposals
    // and suggestions that land against content.
    key: 'ai',
    chips: [
      chip('ai.suggestion.proposed'),
      chip('ai.suggestion.applied'),
      chip('ai.suggestion.dismissed'),
      chip('ai.suggestion.edited'),
      chip('ai.auto_action.proposed'),
    ],
  },
  {
    // "What did an agent do on this task?" — scoped to one (agent, task)
    // pair, which is what a task timeline reader wants.
    key: 'agent',
    chips: [
      chip('agent.task.attached'),
      chip('agent.task.detached'),
      chip('agent.task.thought'),
      chip('agent.task.handoff_to_user'),
      chip('agent.task.handoff_to_agent'),
    ],
  },
  {
    // "Was the agent even running?" — workspace-level operation, the
    // kill switch and run outcomes. Separate because it answers an
    // operational question, not a question about the work.
    key: 'agent_runtime',
    chips: [
      chip('ai.agent.paused'),
      chip('ai.agent.resumed'),
      chip('ai.agent.run.started'),
      chip('ai.agent.run.completed'),
      chip('ai.agent.run.failed'),
    ],
  },
  { key: 'signal', chips: [chip('signal.attached')] },
];

export interface EventFilterBarProps {
  filters: TimelineFilters;
  onChange: (next: TimelineFilters) => void;
  /**
   * Optional workspace id used to populate the actor multi-select. When
   * undefined the picker falls back to a disabled placeholder.
   */
  workspaceId?: string;
}

interface ActorPickerProps {
  workspaceId: string;
  selected: readonly string[];
  onSelect: (ids: string[]) => void;
  label: string;
}

/**
 * Workspace membership can be large, so actors are picked from a checkbox
 * list in a popover rather than a row of chips.
 */
function ActorPicker({ workspaceId, selected, onSelect, label }: ActorPickerProps): ReactElement {
  const { data: users } = useWorkspaceUsersQuery(workspaceId);
  const idPrefix = useId();

  const handleToggle = (id: string, checked: boolean): void => {
    onSelect(checked ? [...selected, id] : selected.filter((v) => v !== id));
  };

  return (
    <Popover
      ariaLabel={label}
      content={
        <div className={styles.actorList}>
          {users.map((u) => (
            <label key={u.id} htmlFor={`${idPrefix}-${u.id}`} className={styles.actorOption}>
              <Checkbox
                id={`${idPrefix}-${u.id}`}
                checked={selected.includes(u.id)}
                onChange={(e) => {
                  handleToggle(u.id, e.target.checked);
                }}
              />
              <span>{u.displayName}</span>
            </label>
          ))}
        </div>
      }
    >
      <Button type="button" variant="default" size="sm" aria-haspopup="dialog">
        <span className={styles.toggleLabel}>
          {label}
          {selected.length > 0 ? <span className={styles.badge}>{selected.length}</span> : null}
        </span>
      </Button>
    </Popover>
  );
}

/** Filters with the kind and actor selections cleared; paging settings stay. */
export function resetFilters(filters: TimelineFilters): TimelineFilters {
  const next: TimelineFilters = {};
  if (filters.limit !== undefined) next.limit = filters.limit;
  if (filters.offset !== undefined) next.offset = filters.offset;
  return next;
}

export default function EventFilterBar({
  filters,
  onChange,
  workspaceId,
}: EventFilterBarProps): ReactElement {
  const { t } = useTranslation('timeline');
  const [open, setOpen] = useState(false);
  const bodyId = useId();

  const selectedKinds = new Set(filters.kind ?? []);
  const isChipActive = (c: KindChip): boolean => c.kinds.every((k) => selectedKinds.has(k));

  const toggleChip = (c: KindChip): void => {
    const next = new Set(selectedKinds);
    const active = isChipActive(c);
    for (const k of c.kinds) {
      if (active) next.delete(k);
      else next.add(k);
    }
    const { kind: Omit, ...rest } = filters;
    onChange(next.size > 0 ? { ...rest, kind: [...next] } : rest);
  };

  const handleActorChange = (ids: string[]): void => {
    const { actor: Omit, ...rest } = filters;
    onChange(ids.length > 0 ? { ...rest, actor: ids } : rest);
  };

  const handleReset = (): void => {
    onChange(resetFilters(filters));
  };

  const actorLabel = t('filter.actor_label', { defaultValue: t('filter.actor_all') });

  const labelFor = (kind: string): string =>
    t(`event_kind.${kind.replace(/\./g, '_')}`, { defaultValue: kind });

  // A chip may select more than one kind, so the badge counts chips
  // rather than kinds — one click must not read as two active filters.
  // Kinds no chip covers can only arrive from restored state; they are
  // counted one by one so the badge never understates what is filtering.
  const chipKinds = new Set(KIND_GROUPS.flatMap((g) => g.chips.flatMap((c) => [...c.kinds])));
  const activeChipCount = KIND_GROUPS.reduce((n, g) => n + g.chips.filter(isChipActive).length, 0);
  const looseKindCount = [...selectedKinds].filter((k) => !chipKinds.has(k)).length;
  const activeFilterCount = activeChipCount + looseKindCount + (filters.actor?.length ?? 0);

  const renderChip = (c: KindChip): ReactElement => (
    <ToggleChip
      key={c.key}
      pressed={isChipActive(c)}
      onPressedChange={() => {
        toggleChip(c);
      }}
    >
      {labelFor(c.key)}
    </ToggleChip>
  );

  return (
    <div className={styles.panel}>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => {
          setOpen(!open);
        }}
      >
        <span className={styles.toggleLabel}>
          <ChevronDown
            size={16}
            aria-hidden="true"
            className={styles.caret}
            data-open={open ? 'true' : 'false'}
          />
          {t('filter.kind_label')}
          {activeFilterCount > 0 ? <span className={styles.badge}>{activeFilterCount}</span> : null}
        </span>
      </Button>

      <Collapse open={open} id={bodyId}>
        <div className={styles.body}>
          {KIND_GROUPS.map((group) => {
            const groupLabel = t(`filter.kind_group.${group.key}`, { defaultValue: group.key });
            return (
              <div key={group.key} className={styles.group}>
                <span className={styles.groupLabel} aria-hidden="true">
                  {groupLabel}
                </span>
                <ToggleChipGroup label={groupLabel}>
                  {group.chips.map((c) => renderChip(c))}
                </ToggleChipGroup>
              </div>
            );
          })}

          <div
            role="group"
            aria-label={actorLabel}
            className={`${styles.group} ${styles.actorGroup}`}
          >
            <span className={styles.groupLabel}>{actorLabel}</span>
            {workspaceId !== undefined ? (
              <Suspense
                fallback={
                  <Input disabled placeholder={t('filter.actor_all')} aria-label={actorLabel} />
                }
              >
                <ActorPicker
                  workspaceId={workspaceId}
                  selected={filters.actor ?? []}
                  onSelect={handleActorChange}
                  label={actorLabel}
                />
              </Suspense>
            ) : (
              <Input disabled placeholder={t('filter.actor_all')} aria-label={actorLabel} />
            )}
          </div>

          <div className={styles.actions}>
            <Button type="button" variant="ghost" onClick={handleReset}>
              {t('filter.reset')}
            </Button>
          </div>
        </div>
      </Collapse>
    </div>
  );
}
