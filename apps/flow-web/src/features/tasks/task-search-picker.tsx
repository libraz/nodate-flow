/**
 * TaskSearchPicker — inline debounced task search inside a workspace.
 * The caller decides what picking a result does and which tasks are offered.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { type ReactElement, type ReactNode, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { type TaskDerivedState, type TaskListItem, useTaskSearch } from './api';
import { STATE_KEY, STATE_TONE } from './constants';
import styles from './task-search-picker.module.css';

export interface TaskSearchPickerProps {
  workspaceId: string;
  /** Tasks never offered (the task itself, existing links). */
  excludeIds: ReadonlySet<string>;
  /** Extra filter over the remaining results. */
  accept?: (task: TaskListItem) => boolean;
  /** Already-translated copy. */
  placeholder: string;
  /** Shown before anything is typed. */
  hint: string;
  emptyText: string;
  cancelText: string;
  /** Control rendered before the search box (e.g. a kind select). */
  leading?: ReactNode;
  /** Line under the controls. */
  description?: ReactNode;
  /** Resolves when the pick is done; a rejection is the caller's to handle. */
  onPick: (taskId: string) => Promise<void>;
  onClose: () => void;
}

export default function TaskSearchPicker({
  workspaceId,
  excludeIds,
  accept,
  placeholder,
  hint,
  emptyText,
  cancelText,
  leading,
  description,
  onPick,
  onClose,
}: TaskSearchPickerProps): ReactElement {
  const { t } = useTranslation('common');
  const [input, setInput] = useState('');
  const [debounced, setDebounced] = useState('');

  useEffect(() => {
    const id = window.setTimeout(() => {
      setDebounced(input.trim());
    }, 200);
    return () => {
      window.clearTimeout(id);
    };
  }, [input]);

  const search = useTaskSearch(workspaceId, debounced, debounced.length > 0);
  const results = (search.data ?? []).filter(
    (task) => !excludeIds.has(task.id) && (accept ? accept(task) : true),
  );
  const stage =
    debounced.length === 0
      ? 'hint'
      : search.isFetching
        ? 'loading'
        : results.length === 0
          ? 'empty'
          : 'results';

  return (
    <div className={styles.picker}>
      <div className={styles.pickerControls}>
        {leading}
        <Input
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
          }}
          placeholder={placeholder}
          autoFocus
          aria-label={placeholder}
          className={styles.pickerSearch}
        />
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          {cancelText}
        </Button>
      </div>
      {description}
      <div key={stage} className={styles.pickerBody}>
        {debounced.length === 0 ? (
          <p className={styles.pickerHint}>{hint}</p>
        ) : search.isFetching ? (
          <div className={styles.pickerLoadingRow} role="status" aria-label={t('common.loading')}>
            <Skeleton className={styles.pickerSkeletonRow} />
            <Skeleton className={styles.pickerSkeletonRow} />
            <Skeleton className={styles.pickerSkeletonRow} />
          </div>
        ) : results.length === 0 ? (
          <p className={styles.pickerHint}>{emptyText}</p>
        ) : (
          <ul className={styles.pickerResults}>
            {results.map((task) => (
              <li key={task.id}>
                <button
                  type="button"
                  onClick={() => {
                    void onPick(task.id);
                  }}
                  className={styles.pickerResultButton}
                >
                  <Badge tone={STATE_TONE[task.derivedState as TaskDerivedState] ?? 'neutral'}>
                    {t(STATE_KEY[task.derivedState as TaskDerivedState] ?? 'tasks.status.open')}
                  </Badge>
                  <span className={styles.pickerResultTitle} title={task.title}>
                    {task.title}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
