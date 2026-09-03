/**
 * ChecklistTab — Checklist pane of the calendar event detail page.
 *
 * Renders the per-event sub-task list with a checkbox + title + delete
 * action per row, plus an inline "add an item" affordance at the
 * bottom (Enter or "Add" button). Done-toggles are optimistic via the
 * mutation's onMutate so the checkbox flips instantly; on error the
 * cache rolls back and the next invalidation reconciles with the
 * authoritative server state.
 *
 * **Drag reordering is intentionally not implemented in this iteration.**
 * The API exposes `sortWeight` on PATCH so a follow-up can wire a
 * drag library (e.g. dnd-kit) without changing the data layer; the
 * minimum viable surface is keyboard-add, toggle-done, and delete.
 *
 * Hooks consumed:
 *   - {@link useEventChecklistQuery}                 — suspense list read
 *   - {@link useAddEventChecklistItemMutation}       — POST a new item
 *   - {@link useUpdateEventChecklistItemMutation}    — PATCH (done / title)
 *   - {@link useDeleteEventChecklistItemMutation}    — DELETE with confirm
 *
 * Wrap in a `<Suspense>` boundary at the call site.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type KeyboardEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import {
  type EventChecklistItem,
  useAddEventChecklistItemMutation,
  useDeleteEventChecklistItemMutation,
  useEventChecklistQuery,
  useUpdateEventChecklistItemMutation,
} from './checklist-api';
import styles from './event-detail-page.module.css';

export interface ChecklistTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

interface ChecklistRowProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  item: EventChecklistItem;
}

function ChecklistRow({ workspaceId, calendarId, eventId, item }: ChecklistRowProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const updateMutation = useUpdateEventChecklistItemMutation();
  const deleteMutation = useDeleteEventChecklistItemMutation();

  const handleToggle = async (): Promise<void> => {
    try {
      await updateMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        itemId: item.id,
        patch: { done: !item.done },
      });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.checklist.update_error'),
      });
    }
  };

  const handleDelete = async (): Promise<void> => {
    const ok = await confirmAction({
      message: t('event.checklist.delete_confirm'),
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await deleteMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        itemId: item.id,
      });
      toaster.show({ tone: 'success', message: t('event.checklist.delete_success') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.checklist.delete_error'),
      });
    }
  };

  return (
    <li className={styles.checklistRow}>
      <Checkbox
        checked={item.done}
        onChange={() => {
          void handleToggle();
        }}
        aria-label={t('event.checklist.toggle_label', { title: item.title })}
        disabled={updateMutation.isPending}
      />
      <span
        className={`${styles.checklistTitle} ${item.done ? styles.checklistTitleDone : ''}`.trim()}
      >
        {item.title}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={deleteMutation.isPending}
        onClick={() => {
          void handleDelete();
        }}
      >
        {t('event.checklist.delete')}
      </Button>
    </li>
  );
}

/**
 * ChecklistTab — see file-level docstring.
 */
export default function ChecklistTab({
  workspaceId,
  calendarId,
  eventId,
}: ChecklistTabProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const { data: items } = useEventChecklistQuery(workspaceId, calendarId, eventId);
  const addMutation = useAddEventChecklistItemMutation();

  const [draft, setDraft] = useState('');
  const trimmed = draft.trim();
  const canSubmit = trimmed.length > 0 && !addMutation.isPending;

  const doneCount = items.filter((i) => i.done).length;

  const handleAdd = async (): Promise<void> => {
    if (!canSubmit) return;
    try {
      await addMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        title: trimmed,
      });
      setDraft('');
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.checklist.add_error'),
      });
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Enter' && canSubmit) {
      e.preventDefault();
      void handleAdd();
    }
  };

  return (
    <div className={styles.tabPanel}>
      {items.length > 0 ? (
        <div className={styles.tabHeader}>
          <span className={styles.checklistProgress}>
            {t('event.checklist.progress', { done: doneCount, total: items.length })}
          </span>
        </div>
      ) : null}
      {items.length === 0 ? (
        <p className={styles.empty}>{t('event.checklist.empty')}</p>
      ) : (
        <ul className={styles.itemList}>
          {items.map((item) => (
            <ChecklistRow
              key={item.id}
              workspaceId={workspaceId}
              calendarId={calendarId}
              eventId={eventId}
              item={item}
            />
          ))}
        </ul>
      )}
      <div className={styles.checklistAddInline}>
        <Input
          type="text"
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
          }}
          onKeyDown={handleKeyDown}
          aria-label={t('event.checklist.add')}
          placeholder={t('event.checklist.add_placeholder')}
        />
        <Button
          type="button"
          disabled={!canSubmit}
          onClick={() => {
            void handleAdd();
          }}
        >
          {t('event.checklist.add')}
        </Button>
      </div>
    </div>
  );
}
