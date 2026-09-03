/**
 * CommentsTab — Comments pane of the calendar event detail page.
 *
 * Renders the threaded discussion attached to a calendar event with an
 * inline-edit / delete UX gated to the comment author, plus an
 * "Add a comment" affordance at the bottom that posts via
 * {@link useAddEventCommentMutation}.
 *
 * Why not reuse {@link import('../tasks/comment-row')}? The task-side
 * row is hard-wired to `useEditTaskComment` / `useDeleteTaskComment`
 * and reads `comment.authorId` / `comment.authorDisplayName`. The
 * event-side `CommentResponse` shape exposes `userId` / `displayName`
 * instead, and naturally needs a different mutation. We mirror the
 * task-side structure (Card → header → body → optional editor) but
 * keep the wiring local.
 *
 * Hooks consumed:
 *   - {@link useEventCommentsQuery}            — suspense list read
 *   - {@link useAddEventCommentMutation}       — POST a new comment
 *   - {@link useEditEventCommentMutation}      — PATCH inline edit
 *   - {@link useDeleteEventCommentMutation}    — DELETE with confirm
 *
 * Wrap in a `<Suspense>` boundary at the call site; this component
 * itself does not fall back.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { selectUser, useAuth } from '../auth/auth-store';
import {
  type EventComment,
  useAddEventCommentMutation,
  useDeleteEventCommentMutation,
  useEditEventCommentMutation,
  useEventCommentsQuery,
} from './comments-api';
import styles from './event-detail-page.module.css';

export interface CommentsTabProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
}

/**
 * Treat `editedAt` as meaningful when present and at least one second
 * past `createdAt`. Mirrors the task-side rule so initial saves with
 * matching timestamps are not flagged as edits.
 */
function isEdited(comment: EventComment): boolean {
  if (comment.editedAt == null) return false;
  return Math.abs(comment.editedAt - comment.createdAt) >= 1;
}

interface CommentRowProps {
  workspaceId: string;
  calendarId: string;
  eventId: string;
  comment: EventComment;
  currentUserId: string | undefined;
  locale: string;
}

function EventCommentRow({
  workspaceId,
  calendarId,
  eventId,
  comment,
  currentUserId,
  locale,
}: CommentRowProps): ReactElement {
  const { t } = useTranslation('calendar-events');
  const editMutation = useEditEventCommentMutation();
  const deleteMutation = useDeleteEventCommentMutation();

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(comment.body);

  const isAuthor = currentUserId != null && comment.userId === currentUserId;
  const edited = isEdited(comment);
  const trimmed = draft.trim();
  const isEmpty = trimmed.length === 0;
  const isUnchanged = trimmed === comment.body;
  const saveDisabled = editMutation.isPending || isEmpty || isUnchanged;
  const timestamp = formatEpochDateTime(comment.createdAt, locale) ?? '';

  const enterEdit = (): void => {
    setDraft(comment.body);
    setEditing(true);
  };

  const cancelEdit = (): void => {
    setEditing(false);
    setDraft(comment.body);
  };

  const handleSave = async (): Promise<void> => {
    if (saveDisabled) return;
    try {
      await editMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        commentId: comment.id,
        body: trimmed,
      });
      setEditing(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.comments.edit_error'),
      });
    }
  };

  const handleDelete = async (): Promise<void> => {
    const ok = await confirmAction({
      message: t('event.comments.delete_confirm'),
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await deleteMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        commentId: comment.id,
      });
      toaster.show({ tone: 'success', message: t('event.comments.delete_success') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'event.comments.delete_error'),
      });
    }
  };

  return (
    <li className={styles.commentCard}>
      <header className={styles.commentHeader}>
        <span className={styles.commentAuthor}>
          {comment.displayName || t('common:common.deactivated_user')}
        </span>
        <span>
          <span className={styles.commentTimestamp}>{timestamp}</span>
          {edited ? <span className={styles.commentEdited}>·</span> : null}
        </span>
      </header>
      {editing ? (
        <div className={styles.addRow}>
          <Textarea
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value);
            }}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault();
                cancelEdit();
              }
            }}
            rows={3}
            autoFocus
            aria-label={t('event.comments.edit')}
          />
          <div className={styles.addRowControls}>
            <Button type="button" variant="ghost" size="sm" onClick={cancelEdit}>
              {t('event.comments.edit_cancel')}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={saveDisabled}
              onClick={() => {
                void handleSave();
              }}
            >
              {t('event.comments.edit_save')}
            </Button>
          </div>
        </div>
      ) : (
        <div className={styles.commentBody}>
          <Markdown>{comment.body}</Markdown>
        </div>
      )}
      {isAuthor && !editing ? (
        <div className={styles.commentActions}>
          <Button type="button" variant="ghost" size="sm" onClick={enterEdit}>
            {t('event.comments.edit')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={deleteMutation.isPending}
            onClick={() => {
              void handleDelete();
            }}
          >
            {t('event.comments.delete')}
          </Button>
        </div>
      ) : null}
    </li>
  );
}

/**
 * CommentsTab — see file-level docstring.
 */
export default function CommentsTab({
  workspaceId,
  calendarId,
  eventId,
}: CommentsTabProps): ReactElement {
  const { t, i18n } = useTranslation('calendar-events');
  const locale = i18n.resolvedLanguage ?? 'en';
  const currentUser = useAuth(selectUser);

  const { data: comments } = useEventCommentsQuery(workspaceId, calendarId, eventId);
  const addMutation = useAddEventCommentMutation();

  const [draft, setDraft] = useState('');
  const trimmed = draft.trim();
  const canSubmit = trimmed.length > 0 && !addMutation.isPending;

  const handleAdd = async (): Promise<void> => {
    if (!canSubmit) return;
    try {
      await addMutation.mutateAsync({
        wsId: workspaceId,
        calId: calendarId,
        evtId: eventId,
        body: trimmed,
      });
      setDraft('');
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'event.comments.add_error') });
    }
  };

  return (
    <div className={styles.tabPanel}>
      {comments.length === 0 ? (
        <p className={styles.empty}>{t('event.comments.empty')}</p>
      ) : (
        <ul className={styles.itemList}>
          {comments.map((comment) => (
            <EventCommentRow
              key={comment.id}
              workspaceId={workspaceId}
              calendarId={calendarId}
              eventId={eventId}
              comment={comment}
              currentUserId={currentUser?.id}
              locale={locale}
            />
          ))}
        </ul>
      )}
      <div className={styles.addRow}>
        <Textarea
          value={draft}
          onChange={(e) => {
            setDraft(e.target.value);
          }}
          rows={3}
          aria-label={t('event.comments.add')}
          placeholder={t('event.comments.add_placeholder')}
        />
        <div className={styles.addRowControls}>
          <Button
            type="button"
            disabled={!canSubmit}
            onClick={() => {
              void handleAdd();
            }}
          >
            {t('event.comments.add')}
          </Button>
        </div>
      </div>
    </div>
  );
}
