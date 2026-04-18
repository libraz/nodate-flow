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

/** Grouped kinds — drives the sectioned chip filter UI. */
const KIND_GROUPS: readonly { key: string; kinds: readonly string[] }[] = [
  { key: 'task', kinds: ['task.created', 'task.updated', 'task.disabled'] },
  {
    key: 'comment',
    kinds: ['task.comment.added', 'task.comment.edited', 'task.comment.removed'],
  },
  { key: 'attachment', kinds: ['task.attachment.added', 'task.attachment.removed'] },
  { key: 'member', kinds: ['task.actor.added', 'task.actor.removed'] },
  { key: 'dependency', kinds: ['task.dependency.added', 'task.dependency.removed'] },
  { key: 'constraint', kinds: ['task.constraint.added', 'task.constraint.removed'] },
  {
    key: 'transition',
    kinds: [
      'task.transition.start',
      'task.transition.block',
      'task.transition.unblock',
      'task.transition.submit',
      'task.transition.complete',
      'task.transition.reopen',
      'task.transition.cancel',
    ],
  },
  { key: 'signal', kinds: ['signal.attached'] },
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
        minInlineSize: '14rem',
        minBlockSize: '6rem',
        padding: '0.25rem',
        borderRadius: '0.25rem',
        border: '1px solid var(--color-border)',
        background: 'var(--color-bg)',
        color: 'var(--color-fg)',
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
  const toggleKind = (kind: string): void => {
    const next = new Set(selectedKinds);
    if (next.has(kind)) next.delete(kind);
    else next.add(kind);
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

  const renderChip = (kind: string): ReactElement => {
    const active = selectedKinds.has(kind);
    return (
      <button
        key={kind}
        type="button"
        aria-pressed={active}
        onClick={() => {
          toggleKind(kind);
        }}
        style={{
          padding: '0.25rem 0.625rem',
          borderRadius: '999px',
          border: `1px solid ${active ? 'var(--color-accent, #9b59b6)' : 'var(--color-border)'}`,
          background: active
            ? 'var(--color-accent-subtle, rgba(155,89,182,0.12))'
            : 'var(--color-surface, transparent)',
          color: active ? 'var(--color-accent, #9b59b6)' : 'var(--color-fg)',
          fontSize: '0.8125rem',
          cursor: 'pointer',
        }}
      >
        {labelFor(kind)}
      </button>
    );
  };

  return (
    <details
      style={{
        border: '1px solid var(--color-border)',
        borderRadius: '0.5rem',
        padding: '0.5rem 0.75rem',
        background: 'var(--color-surface, transparent)',
      }}
    >
      <summary
        style={{
          cursor: 'pointer',
          fontSize: '0.8125rem',
          color: 'var(--color-muted)',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
        }}
      >
        {t('filter.kind_label')}
        {selectedKinds.size > 0 || (filters.actor?.length ?? 0) > 0 ? (
          <span
            style={{
              padding: '0 0.5rem',
              borderRadius: '999px',
              background: 'var(--color-accent-subtle, rgba(155,89,182,0.15))',
              color: 'var(--color-accent, #9b59b6)',
              fontSize: '0.6875rem',
            }}
          >
            {selectedKinds.size + (filters.actor?.length ?? 0)}
          </span>
        ) : null}
      </summary>

      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: '0.625rem',
          paddingBlockStart: '0.75rem',
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
              columnGap: '0.75rem',
              rowGap: '0.25rem',
            }}
          >
            <span
              style={{
                fontSize: '0.6875rem',
                textTransform: 'uppercase',
                letterSpacing: '0.05em',
                color: 'var(--color-muted)',
              }}
            >
              {t(`filter.kind_group.${group.key}`, { defaultValue: group.key })}
            </span>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.375rem' }}>
              {group.kinds.map((k) => renderChip(k))}
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
            columnGap: '0.75rem',
            marginBlockStart: '0.25rem',
            paddingBlockStart: '0.625rem',
            borderBlockStart: '1px solid var(--color-border)',
          }}
        >
          <span
            style={{
              fontSize: '0.6875rem',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              color: 'var(--color-muted)',
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
