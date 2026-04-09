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
import { TIMELINE_KINDS, type TimelineFilters } from './api';

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

  return (
    <div
      style={{
        display: 'flex',
        gap: '0.75rem',
        alignItems: 'flex-end',
        flexWrap: 'wrap',
        padding: '0.5rem 0',
      }}
    >
      <div
        role="group"
        aria-label={t('filter.kind_label')}
        style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem', fontSize: '0.75rem' }}
      >
        <span style={{ color: 'var(--color-muted)' }}>{t('filter.kind_label')}</span>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.375rem', maxInlineSize: '32rem' }}>
          {(() => {
            const seen = new Set<string>();
            const options: { kind: string; label: string }[] = [];
            for (const kind of TIMELINE_KINDS) {
              const label = t(`event_kind.${kind.replace(/\./g, '_')}`, { defaultValue: kind });
              if (seen.has(label)) continue;
              seen.add(label);
              options.push({ kind, label });
            }
            return options.map((o) => {
              const active = selectedKinds.has(o.kind);
              return (
                <button
                  key={o.kind}
                  type="button"
                  aria-pressed={active}
                  onClick={() => {
                    toggleKind(o.kind);
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
                  {o.label}
                </button>
              );
            });
          })()}
        </div>
      </div>

      <div
        style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem', fontSize: '0.75rem' }}
        role="group"
        aria-label={actorLabel}
      >
        <span style={{ color: 'var(--color-muted)' }}>{actorLabel}</span>
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

      <Button type="button" variant="ghost" onClick={handleReset}>
        {t('filter.reset')}
      </Button>
    </div>
  );
}
