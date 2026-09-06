/**
 * EventFilterBar — kind multi-select + workspace-scoped actor picker + reset.
 *
 * The actor picker is rendered only when a workspaceId is supplied; project
 * timelines that don't yet thread their parent workspace fall back to the
 * disabled placeholder used during F0.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import { type ChangeEvent, type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceUsersQuery } from '../workspaces/api';
import type { TimelineFilters } from './api';

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
    chips: [chip('task.constraint.added'), chip('task.constraint.removed')],
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
    // The only calendar kind the backend actually emits: the reminder
    // the scheduler appends when a calendar event comes due. The rest of
    // the `calendar.*` vocabulary is declared but unemitted, so it gets
    // no chip under the rule above. The group is named for the domain
    // rather than for the reminder so the rest of the family can join
    // this section once it has a producer, without moving the chip.
    key: 'calendar',
    chips: [chip('calendar.reminder')],
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

function ActorPicker({ workspaceId, selected, onSelect, label }: ActorPickerProps): ReactElement {
  const { data: users } = useWorkspaceUsersQuery(workspaceId);

  const handleChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const ids = Array.from(e.target.selectedOptions, (o) => o.value);
    onSelect(ids);
  };

  return (
    <select
      multiple
      value={[...selected]}
      onChange={handleChange}
      aria-label={label}
      style={{
        // nf-token-override: component dimension, not a spacing step
        minInlineSize: '14rem',
        // nf-token-override: component dimension, not a spacing step
        minBlockSize: '6rem',
        padding: 'var(--nf-space-1)',
        borderRadius: 'var(--nf-radius-xs)',
        border: '1px solid var(--nf-color-border)',
        background: 'var(--nf-color-bg)',
        color: 'var(--nf-color-fg)',
      }}
    >
      {users.map((u) => (
        <option key={u.id} value={u.id}>
          {u.displayName}
        </option>
      ))}
    </select>
  );
}

export default function EventFilterBar({
  filters,
  onChange,
  workspaceId,
}: EventFilterBarProps): ReactElement {
  const { t } = useTranslation('timeline');

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
    const next: TimelineFilters = {};
    if (filters.limit !== undefined) next.limit = filters.limit;
    if (filters.offset !== undefined) next.offset = filters.offset;
    onChange(next);
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

  const renderChip = (c: KindChip): ReactElement => {
    const active = isChipActive(c);
    return (
      <button
        key={c.key}
        type="button"
        aria-pressed={active}
        onClick={() => {
          toggleChip(c);
        }}
        style={{
          padding: 'var(--nf-space-1) var(--nf-space-2-5)',
          borderRadius: 'var(--nf-radius-pill)',
          border: `1px solid ${active ? 'var(--nf-color-accent)' : 'var(--nf-color-border)'}`,
          background: active ? 'var(--nf-color-accent-subtle)' : 'var(--nf-color-surface)',
          color: active ? 'var(--nf-color-accent-fg)' : 'var(--nf-color-fg)',
          fontSize: 'var(--nf-text-supporting)',
          cursor: 'pointer',
        }}
      >
        {labelFor(c.key)}
      </button>
    );
  };

  return (
    <details
      style={{
        border: '1px solid var(--nf-color-border)',
        borderRadius: 'var(--nf-radius-md)',
        padding: 'var(--nf-space-2) var(--nf-space-3)',
        background: 'var(--nf-color-surface)',
      }}
    >
      <summary
        style={{
          cursor: 'pointer',
          fontSize: 'var(--nf-text-supporting)',
          color: 'var(--nf-color-fg-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--nf-space-2)',
        }}
      >
        {t('filter.kind_label')}
        {activeFilterCount > 0 ? (
          <span
            style={{
              padding: '0 var(--nf-space-2)',
              borderRadius: 'var(--nf-radius-pill)',
              background: 'var(--nf-color-accent-subtle)',
              color: 'var(--nf-color-accent-fg)',
              fontSize: 'var(--nf-text-micro)',
            }}
          >
            {activeFilterCount}
          </span>
        ) : null}
      </summary>

      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-2-5)',
          paddingBlockStart: 'var(--nf-space-3)',
        }}
      >
        {KIND_GROUPS.map((group) => (
          <div
            key={group.key}
            role="group"
            aria-label={t(`filter.kind_group.${group.key}`, { defaultValue: group.key })}
            style={{
              display: 'grid',
              gridTemplateColumns: '7rem 1fr',
              alignItems: 'center',
              columnGap: 'var(--nf-space-3)',
              rowGap: 'var(--nf-space-1)',
            }}
          >
            <span
              style={{
                fontSize: 'var(--nf-text-micro)',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {t(`filter.kind_group.${group.key}`, { defaultValue: group.key })}
            </span>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--nf-space-1-5)' }}>
              {group.chips.map((c) => renderChip(c))}
            </div>
          </div>
        ))}

        <div
          role="group"
          aria-label={actorLabel}
          style={{
            display: 'grid',
            gridTemplateColumns: '7rem 1fr',
            alignItems: 'center',
            columnGap: 'var(--nf-space-3)',
            marginBlockStart: 'var(--nf-space-1)',
            paddingBlockStart: 'var(--nf-space-2-5)',
            borderBlockStart: '1px solid var(--nf-color-border)',
          }}
        >
          <span
            style={{
              fontSize: 'var(--nf-text-micro)',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {actorLabel}
          </span>
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

        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button type="button" variant="ghost" onClick={handleReset}>
            {t('filter.reset')}
          </Button>
        </div>
      </div>
    </details>
  );
}
