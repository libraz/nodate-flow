/**
 * CalendarMemosPanel — per-calendar memos surface.
 *
 * Integration shape: **Option A — drawer**. The panel mounts inside
 * the existing Calendar Settings Drawer (the same chrome the
 * Members / General tabs live in), triggered from a new "Memos" entry
 * on the calendars-rail per-calendar 3-dot menu. Adding a fourth
 * column to `/calendar` would have crowded the layout, and the drawer
 * already owns focus-trap / portal / Escape semantics — exactly what
 * a small side surface needs. Picking the same chrome also keeps the
 * other settings tabs visually consistent.
 *
 * Each row is a titled todo:
 *   - `<input type="checkbox">` for `done`. PATCHes immediately on
 *     toggle (optimistic update applied in `useUpdateMemoMutation`).
 *   - Editable title `<input>`. Debounced 800ms — keystrokes update
 *     local state immediately, a `setTimeout` PATCHes 800ms after the
 *     last edit. The pending timer is cleared on unmount and on
 *     subsequent edits. A blur PATCHes synchronously so a fast
 *     close-and-move-on does not lose the last keystroke.
 *   - Expandable notes `body`. A single-line row is too cramped for
 *     multi-line notes, so the body lives behind an expander toggle
 *     rather than a modal: the panel is an inline edit-in-place surface
 *     (and already sits inside the drawer's focus-trap), so a Dialog
 *     would fight that chrome and break the autosave rhythm. The
 *     expander reveals a `<Textarea>` that shares the row's per-field
 *     draft + debounced autosave + blur/unmount flush lifecycle, mirror
 *     of the title path. A subtle dot on the toggle hints that a
 *     collapsed row carries body text.
 *   - Ghost Delete button. Optimistic removal.
 *
 * The bottom add-row (input + Add button, Enter submits) creates a
 * new memo with `sortWeight = (count + 1)` so the row renders at the
 * end of the existing list. The field clears on success; failures
 * surface as a danger toast.
 *
 * Sorting: ascending by `sortWeight`, then ascending by `createdAt`
 * as a tiebreaker. Drag reordering is intentionally **out of scope**
 * for now — there's no sortable-list helper in the repo today and a
 * from-scratch DnD setup would distract from the core editor loop.
 * The backend's `sortWeight` field is already PATCHable when a future
 * iteration wants to add it.
 *
 * SSE: realtime invalidation lands automatically because the cache
 * key sits under `['calendars', wsId]`, which the existing realtime
 * mapper already invalidates on `calendar.changed`. No extra wiring
 * needed.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import {
  type FormEvent,
  type ReactElement,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import {
  type Memo,
  useCreateMemoMutation,
  useDeleteMemoMutation,
  useMemosQuery,
  useUpdateMemoMutation,
} from './api';
import styles from './calendar-memos-panel.module.css';

/** Debounce window for the title autosave, in milliseconds. */
const TITLE_AUTOSAVE_MS = 800;

/**
 * Debounce window for the body autosave, in milliseconds. Kept equal to
 * the title window so both fields on a row feel identical.
 */
const BODY_AUTOSAVE_MS = 800;

export interface CalendarMemosPanelProps {
  workspaceId: string;
  calendarId: string;
}

/**
 * Order memos by `sortWeight` ascending, with `createdAt` as a stable
 * tiebreaker so two memos created with the same weight don't shuffle
 * on every render.
 */
function sortMemos(memos: Memo[]): Memo[] {
  return [...memos].sort((a, b) => {
    if (a.sortWeight !== b.sortWeight) return a.sortWeight - b.sortWeight;
    return a.createdAt - b.createdAt;
  });
}

export default function CalendarMemosPanel({
  workspaceId,
  calendarId,
}: CalendarMemosPanelProps): ReactElement {
  const { t } = useTranslation('common');
  const memosQuery = useMemosQuery(workspaceId, calendarId);
  const createMemo = useCreateMemoMutation();
  const updateMemo = useUpdateMemoMutation();
  const deleteMemo = useDeleteMemoMutation();

  const [draftTitle, setDraftTitle] = useState('');

  const handleAdd = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const trimmed = draftTitle.trim();
    if (trimmed.length === 0) return;
    const nextWeight = (memosQuery.data?.length ?? 0) + 1;
    createMemo.mutate(
      { wsId: workspaceId, calId: calendarId, title: trimmed, sortWeight: nextWeight },
      {
        onSuccess: () => {
          setDraftTitle('');
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'calendar.memos.add_error'),
          });
        },
      },
    );
  };

  if (memosQuery.isPending) {
    return (
      <div className={styles.body}>
        <div className={styles.skeleton}>
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: '2.5rem', inlineSize: '100%' }} />
        </div>
      </div>
    );
  }

  const memos = sortMemos(memosQuery.data ?? []);

  return (
    <div className={styles.body}>
      {memos.length === 0 ? (
        <p className={styles.empty}>{t('calendar.memos.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {memos.map((memo) => (
            <MemoRow
              key={memo.id}
              memo={memo}
              workspaceId={workspaceId}
              calendarId={calendarId}
              onUpdate={(body) =>
                updateMemo.mutate(
                  { wsId: workspaceId, calId: calendarId, memoId: memo.id, body },
                  {
                    onError: (err) => {
                      toaster.show({
                        tone: 'danger',
                        message: formatApiError(err, t, 'calendar.memos.update_error'),
                      });
                    },
                  },
                )
              }
              onDelete={() => {
                void (async () => {
                  const ok = await confirmAction({
                    title: t('calendar.memos.title'),
                    message: t('calendar.memos.delete_confirm'),
                    tone: 'danger',
                    confirmLabel: t('calendar.memos.delete'),
                  });
                  if (!ok) return;
                  deleteMemo.mutate(
                    { wsId: workspaceId, calId: calendarId, memoId: memo.id },
                    {
                      onSuccess: () => {
                        toaster.show({
                          tone: 'success',
                          message: t('calendar.memos.delete_success'),
                        });
                      },
                      onError: (err) => {
                        toaster.show({
                          tone: 'danger',
                          message: formatApiError(err, t, 'calendar.memos.delete_error'),
                        });
                      },
                    },
                  );
                })();
              }}
            />
          ))}
        </ul>
      )}

      <form className={styles.addRow} onSubmit={handleAdd}>
        <Input
          aria-label={t('calendar.memos.title_label')}
          className={styles.addInput}
          placeholder={t('calendar.memos.add_placeholder')}
          value={draftTitle}
          onChange={(e) => setDraftTitle(e.target.value)}
          maxLength={200}
        />
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={draftTitle.trim().length === 0 || createMemo.isPending}
        >
          {t('calendar.memos.add')}
        </Button>
      </form>
    </div>
  );
}

interface MemoRowProps {
  memo: Memo;
  workspaceId: string;
  calendarId: string;
  /** Patch dispatcher; the parent owns the mutation so toast state stays in one place. */
  onUpdate: (patch: { title?: string; done?: boolean; body?: string }) => void;
  onDelete: () => void;
}

/** Max length for the multi-line memo body. */
const BODY_MAX_LENGTH = 4000;

/**
 * A debounced, flush-on-blur/unmount autosave for a single text field on
 * a memo row. Owns the local draft so keystrokes coalesce without the
 * cursor bouncing when the cache row is replaced (server echo, SSE
 * invalidation, optimistic merge), and only emits a patch when the
 * trimmed/normalized value actually changed.
 *
 * @param canonical Server-truth value for the field.
 * @param debounceMs Debounce window before the autosave fires.
 * @param save Emit the field patch. Receives the normalized value.
 * @param normalize Maps the raw draft to the value sent to the server
 *   and compared against the canonical snapshot (e.g. trim).
 */
function useFieldAutosave(
  canonical: string,
  debounceMs: number,
  save: (value: string) => void,
  normalize: (raw: string) => string,
): {
  value: string;
  onChange: (next: string) => void;
  flush: () => void;
} {
  const [value, setValue] = useState(canonical);
  const canonicalRef = useRef(canonical);
  const draftRef = useRef(value);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Keep draftRef in sync so the blur / unmount flush always sees the
  // latest user input without re-binding the handler.
  draftRef.current = value;

  // When the server echo arrives (or another tab edits the row), replace
  // the local draft with the canonical value — but only when no edit is
  // mid-flight (timer pending), so an in-flight keystroke is not stomped
  // by an older snapshot.
  useEffect(() => {
    if (timerRef.current !== null) return;
    if (canonical !== canonicalRef.current) {
      canonicalRef.current = canonical;
      setValue(canonical);
    }
  }, [canonical]);

  const commit = useCallback(() => {
    const next = normalize(draftRef.current);
    if (next === canonicalRef.current) return;
    canonicalRef.current = next;
    save(next);
  }, [normalize, save]);

  /**
   * Flush the pending edit immediately. Called on blur and on unmount so
   * a fast close-and-navigate does not silently drop the last keystroke.
   * No-op when no edit is queued.
   */
  const flush = useCallback(() => {
    if (timerRef.current === null) return;
    clearTimeout(timerRef.current);
    timerRef.current = null;
    commit();
  }, [commit]);

  // Flush on unmount so closing the drawer mid-edit still saves.
  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
        commit();
      }
    };
  }, [commit]);

  const onChange = useCallback(
    (next: string): void => {
      setValue(next);
      if (timerRef.current !== null) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        commit();
      }, debounceMs);
    },
    [commit, debounceMs],
  );

  return { value, onChange, flush };
}

/**
 * One memo row. Owns the local title and body drafts so debounced
 * autosave can coalesce keystrokes without bouncing the cursor when the
 * cache row is replaced. The body lives behind an expander; a collapsed
 * row with body text shows a subtle dot on the toggle.
 */
function MemoRow({ memo, onUpdate, onDelete }: MemoRowProps): ReactElement {
  const { t } = useTranslation('common');
  const bodyId = useId();
  const isOptimistic = memo.id.startsWith('optimistic-');
  const hasBody = (memo.body ?? '').trim().length > 0;
  // `null` = follow `hasBody` (open when body exists, collapsed otherwise);
  // `true` / `false` = user took explicit control of the expander.
  const [expandIntent, setExpandIntent] = useState<boolean | null>(null);
  const showBody = expandIntent ?? hasBody;

  const titleField = useFieldAutosave(
    memo.title,
    TITLE_AUTOSAVE_MS,
    useCallback(
      (next: string) => {
        // Title is required; never autosave it empty.
        if (next.length === 0) return;
        onUpdate({ title: next });
      },
      [onUpdate],
    ),
    useCallback((raw: string) => raw.trim(), []),
  );

  const bodyField = useFieldAutosave(
    memo.body ?? '',
    BODY_AUTOSAVE_MS,
    useCallback(
      (next: string) => {
        // Body is optional; an emptied body sends an empty string so the
        // backend can clear it.
        onUpdate({ body: next });
      },
      [onUpdate],
    ),
    // Preserve interior whitespace/newlines; only trim the outer edges.
    useCallback((raw: string) => raw.trim(), []),
  );

  const handleToggleDone = (): void => {
    onUpdate({ done: !memo.done });
  };

  const attribution = memo.userDisplayName.trim();

  return (
    <li className={styles.row}>
      <input
        type="checkbox"
        className={styles.checkbox}
        checked={memo.done}
        onChange={handleToggleDone}
        disabled={isOptimistic}
        aria-label={t('calendar.memos.checkbox_label', { title: memo.title })}
      />
      <div className={styles.bodyText}>
        <div className={styles.titleLine}>
          <input
            type="text"
            className={`${styles.titleInput}${memo.done ? ` ${styles.titleInputDone}` : ''}`}
            aria-label={t('calendar.memos.title_label')}
            value={titleField.value}
            onChange={(e) => titleField.onChange(e.target.value)}
            onBlur={titleField.flush}
            disabled={isOptimistic}
            maxLength={200}
          />
          <button
            type="button"
            className={styles.expandToggle}
            onClick={() => setExpandIntent(!showBody)}
            aria-expanded={showBody}
            aria-controls={bodyId}
            disabled={isOptimistic}
            aria-label={
              showBody
                ? t('calendar.memos.body_collapse')
                : hasBody
                  ? t('calendar.memos.body_show')
                  : t('calendar.memos.body_add')
            }
            title={
              showBody
                ? t('calendar.memos.body_collapse')
                : hasBody
                  ? t('calendar.memos.body_show')
                  : t('calendar.memos.body_add')
            }
          >
            <span aria-hidden="true" className={styles.expandIcon}>
              {showBody ? '−' : '+'}
            </span>
            {hasBody && !showBody ? <span aria-hidden="true" className={styles.bodyDot} /> : null}
          </button>
        </div>
        {showBody ? (
          <Textarea
            id={bodyId}
            className={styles.bodyTextarea}
            aria-label={t('calendar.memos.body_label')}
            placeholder={t('calendar.memos.body_placeholder')}
            value={bodyField.value}
            onChange={(e) => bodyField.onChange(e.target.value)}
            onBlur={bodyField.flush}
            disabled={isOptimistic}
            maxLength={BODY_MAX_LENGTH}
            rows={3}
          />
        ) : null}
        {attribution.length > 0 ? (
          <span className={styles.attribution}>
            {t('calendar.memos.author_attribution', { name: attribution })}
          </span>
        ) : null}
      </div>
      <div className={styles.actions}>
        <Button type="button" variant="ghost" size="sm" onClick={onDelete} disabled={isOptimistic}>
          {t('calendar.memos.delete')}
        </Button>
      </div>
    </li>
  );
}
