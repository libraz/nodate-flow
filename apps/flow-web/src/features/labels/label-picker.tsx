/**
 * LabelPicker — popover with search to add/remove labels from a task.
 *
 * Typing a name that matches no label exactly offers a trailing "create"
 * row. Enter picks the highlighted row (the first one until arrows move it).
 */

import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import { Check, Plus } from 'lucide-react';
import {
  type CSSProperties,
  type KeyboardEvent,
  type MouseEvent,
  type ReactElement,
  useId,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { Label } from './api';
import { pickLabelColor } from './label-color';
import styles from './label-picker.module.css';

/** Mirrors the backend limit on label names. */
const NAME_MAX = 64;

export interface LabelPickerProps {
  /** All workspace labels. */
  labels: Label[];
  /** IDs of labels currently applied to the task. */
  activeIds: readonly string[];
  onToggle: (labelId: string, active: boolean) => void;
  /** Creates a label named `name` and applies it; omit to hide the create row. */
  onCreate?: (name: string) => void;
  disabled?: boolean;
}

type Row = { kind: 'label'; label: Label } | { kind: 'create'; name: string };

export default function LabelPicker({
  labels,
  activeIds,
  onToggle,
  onCreate,
  disabled,
}: LabelPickerProps): ReactElement {
  const { t } = useTranslation('labels');
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [cursor, setCursor] = useState(0);
  const activeSet = new Set(activeIds);

  const query = search.trim();
  const q = query.toLowerCase();
  const rows: Row[] = (q ? labels.filter((l) => l.name.toLowerCase().includes(q)) : labels).map(
    (label) => ({ kind: 'label', label }),
  );
  if (onCreate && query && !labels.some((l) => l.name.toLowerCase() === q)) {
    rows.push({ kind: 'create', name: query });
  }
  const current = Math.min(cursor, rows.length - 1);
  const rowId = (index: number): string => `${listId}-${index}`;

  const choose = (row: Row): void => {
    if (row.kind === 'create') {
      onCreate?.(row.name);
      setSearch('');
      setCursor(0);
      setOpen(false);
      return;
    }
    onToggle(row.label.id, !activeSet.has(row.label.id));
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (rows.length === 0) return;
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setCursor((Math.max(current, 0) + step + rows.length) % rows.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const row = rows[Math.max(current, 0)];
      if (row) choose(row);
    }
  };

  const hasApplied = activeIds.length > 0;
  const emptyKey = labels.length === 0 && !query ? 'picker.no_labels' : 'picker.empty';

  const dotStyle = (color: string): CSSProperties => ({ '--dot-color': color }) as CSSProperties;

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          setSearch('');
          setCursor(0);
        }
      }}
      content={
        <div className={styles.root}>
          <Input
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={rows.length > 0 ? rowId(current) : undefined}
            maxLength={NAME_MAX}
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setCursor(0);
            }}
            onKeyDown={handleKeyDown}
            placeholder={t('picker.search_placeholder')}
            aria-label={t('picker.search_placeholder')}
          />
          {rows.length === 0 ? (
            <p className={styles.message}>{t(emptyKey)}</p>
          ) : (
            <ul id={listId} role="listbox" className={styles.list} aria-label={t('picker.title')}>
              {rows.map((row, index) => {
                const rowProps = {
                  id: rowId(index),
                  'data-active': index === current,
                  onMouseDown: (e: MouseEvent<HTMLLIElement>) => e.preventDefault(),
                  onClick: () => choose(row),
                } as const;
                if (row.kind === 'create') {
                  return (
                    <li
                      key="create"
                      role="option"
                      {...rowProps}
                      aria-selected={false}
                      className={`${styles.row} ${styles.create}`}
                    >
                      <Plus size={14} aria-hidden className={styles.createIcon} />
                      <span className={styles.name} title={row.name}>
                        {t('picker.create_new', { name: row.name })}
                      </span>
                      <span
                        className={styles.dot}
                        style={dotStyle(pickLabelColor(row.name))}
                        aria-hidden="true"
                      />
                    </li>
                  );
                }
                const { label } = row;
                const active = activeSet.has(label.id);
                return (
                  <li
                    key={label.id}
                    role="option"
                    {...rowProps}
                    aria-selected={active}
                    className={styles.row}
                  >
                    <span className={styles.dot} style={dotStyle(label.color)} aria-hidden="true" />
                    <span className={styles.name} title={label.name}>
                      {label.name}
                    </span>
                    {active ? <Check size={14} aria-hidden className={styles.check} /> : null}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      }
    >
      <button
        type="button"
        disabled={disabled}
        aria-label={t('task.add')}
        className={`${styles.trigger}${hasApplied ? ` ${styles.triggerIconOnly}` : ''}`}
      >
        <Plus size={12} aria-hidden />
        {hasApplied ? null : t('task.add')}
      </button>
    </Popover>
  );
}
