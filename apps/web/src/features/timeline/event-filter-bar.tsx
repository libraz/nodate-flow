/**
 * EventFilterBar — kind multi-select + (placeholder) actor picker + reset.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import type { ChangeEvent, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { TIMELINE_KINDS, type TimelineFilters } from './api';

export interface EventFilterBarProps {
  filters: TimelineFilters;
  onChange: (next: TimelineFilters) => void;
}

export default function EventFilterBar({ filters, onChange }: EventFilterBarProps): ReactElement {
  const { t } = useTranslation('timeline');

  const handleKindChange = (e: ChangeEvent<HTMLSelectElement>): void => {
    const selected = Array.from(e.target.selectedOptions, (o) => o.value);
    const { kind: Omit, ...rest } = filters;
    onChange(selected.length > 0 ? { ...rest, kind: selected } : rest);
  };

  const handleReset = (): void => {
    const next: TimelineFilters = {};
    if (filters.limit !== undefined) next.limit = filters.limit;
    if (filters.offset !== undefined) next.offset = filters.offset;
    onChange(next);
  };

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
      <label
        style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem', fontSize: '0.75rem' }}
      >
        <span style={{ color: 'var(--color-muted)' }}>{t('filter.kind_label')}</span>
        <select
          multiple
          value={filters.kind ? [...filters.kind] : []}
          onChange={handleKindChange}
          aria-label={t('filter.kind_label')}
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
          {TIMELINE_KINDS.map((kind) => (
            <option key={kind} value={kind}>
              {t(`event.${kind.replace(/\./g, '_')}`, { actor: '', defaultValue: kind })}
            </option>
          ))}
        </select>
      </label>

      {/* TODO: replace with a workspace-scoped user picker once the
          /workspaces/{wsId}/users endpoint exists. */}
      <div
        style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem', fontSize: '0.75rem' }}
      >
        <span style={{ color: 'var(--color-muted)' }}>{t('filter.actor_all')}</span>
        <Input disabled placeholder={t('filter.actor_all')} aria-label={t('filter.actor_all')} />
      </div>

      <Button type="button" variant="ghost" onClick={handleReset}>
        {t('filter.reset')}
      </Button>
    </div>
  );
}
