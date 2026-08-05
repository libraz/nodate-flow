/**
 * LabelPicker — popover with search to add/remove labels from a task.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Popover from '@nodate-flow/ui/primitives/popover';
import { type ReactElement, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { Label } from './api';

export interface LabelPickerProps {
  /** All workspace labels. */
  labels: Label[];
  /** IDs of labels currently applied to the task. */
  activeIds: readonly string[];
  onToggle: (labelId: string, active: boolean) => void;
  disabled?: boolean;
}

export default function LabelPicker({
  labels,
  activeIds,
  onToggle,
  disabled,
}: LabelPickerProps): ReactElement {
  const { t } = useTranslation('labels');
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const activeSet = useMemo(() => new Set(activeIds), [activeIds]);

  const filtered = useMemo(() => {
    const q = search.toLowerCase().trim();
    if (!q) return labels;
    return labels.filter((l) => l.name.toLowerCase().includes(q));
  }, [labels, search]);

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setSearch('');
      }}
      content={
        <div className="flex flex-col gap-2 p-2" style={{ minWidth: '14rem' }}>
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('picker.search_placeholder')}
            className="w-full rounded-md border border-[var(--nf-color-border)] bg-[var(--nf-color-bg)] px-2 py-1 text-sm outline-none focus:ring-2 focus:ring-[var(--nf-color-focus-ring)]"
          />
          {filtered.length === 0 ? (
            <p className="px-2 py-1 text-sm text-[var(--nf-color-fg-muted)]">{t('picker.empty')}</p>
          ) : (
            <ul role="listbox" className="flex flex-col gap-0.5" aria-label={t('picker.title')}>
              {filtered.map((label) => {
                const active = activeSet.has(label.id);
                return (
                  <li key={label.id} role="option" aria-selected={active}>
                    <button
                      type="button"
                      onClick={() => onToggle(label.id, !active)}
                      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-[var(--nf-color-surface-hover)]"
                    >
                      <span
                        className="inline-block h-3 w-3 rounded-full flex-shrink-0"
                        style={{ backgroundColor: label.color }}
                        aria-hidden="true"
                      />
                      <span className="flex-1 text-left">{label.name}</span>
                      {active ? (
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          viewBox="0 0 16 16"
                          fill="currentColor"
                          className="h-4 w-4 text-[var(--nf-color-success)]"
                          aria-hidden="true"
                        >
                          <path
                            fillRule="evenodd"
                            d="M12.416 3.376a.75.75 0 0 1 .208 1.04l-5 7.5a.75.75 0 0 1-1.154.114l-3-3a.75.75 0 0 1 1.06-1.06l2.353 2.353 4.493-6.74a.75.75 0 0 1 1.04-.207Z"
                            clipRule="evenodd"
                          />
                        </svg>
                      ) : null}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      }
    >
      <Button variant="ghost" size="sm" disabled={disabled} aria-label={t('task.add')}>
        {t('task.add')}
      </Button>
    </Popover>
  );
}
