/**
 * CommentRow — single task comment card with author-only edit / delete UX.
 *
 * Default view mirrors the pre-existing layout (author, timestamp, markdown
 * body). When the signed-in viewer is the comment's author, a kebab menu is
 * rendered that exposes Edit / Delete actions. Edit swaps the body into an
 * inline textarea with Save / Cancel, matching the TitleEditor / Description
 * inline-edit pattern already in use on the task detail page. Delete opens
 * the shared confirm dialog before firing the mutation.
 *
 * Backend enforcement is authoritative: only the author can PATCH, and
 * author-or-workspace-admin can DELETE. The UI simply gates the affordance
 * to the author so the common case is clean; callers relying on admin
 * delete are expected to use an admin console rather than this row.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import Popover from '@nodate-flow/ui/primitives/popover';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { MoreHorizontal } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { type TaskComment, useDeleteTaskComment, useEditTaskComment } from './api';
import styles from './comment-row.module.css';
import MarkdownEditor from './markdown-editor';

export interface CommentRowProps {
  /** Task public id this comment belongs to. */
  taskId: string;
  /** Workspace whose members can be named with `@` while editing. */
  workspaceId: string;
  /** The comment to render. */
  comment: TaskComment;
  /** Signed-in viewer's public id, or `undefined` when unknown. */
  currentUserId: string | undefined;
  /** BCP-47 language tag used for formatting the timestamp. */
  locale: string;
}

/**
 * Treat `editedAt` as meaningful when it is present and differs from
 * `createdAt` by at least one second. The backend stamps both fields at
 * creation time on some paths; the one-second guard avoids flagging the
 * initial save as an edit.
 */
function isEdited(comment: TaskComment): boolean {
  if (comment.editedAt == null) return false;
  return Math.abs(comment.editedAt - comment.createdAt) >= 1;
}

/**
 * Renders a single comment. See {@link CommentRowProps}.
 */
export default function CommentRow({
  taskId,
  workspaceId,
  comment,
  currentUserId,
  locale,
}: CommentRowProps): ReactElement {
  const { t } = useTranslation('common');
  const edit = useEditTaskComment();
  const del = useDeleteTaskComment();
  const [menuOpen, setMenuOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(comment.body);

  const isAuthor = currentUserId != null && comment.authorId === currentUserId;
  const edited = isEdited(comment);

  const enterEdit = (): void => {
    setDraft(comment.body);
    setEditing(true);
    setMenuOpen(false);
  };

  const cancelEdit = (): void => {
    setEditing(false);
    setDraft(comment.body);
  };

  const trimmed = draft.trim();
  const isEmpty = trimmed.length === 0;
  const isUnchanged = trimmed === comment.body;
  const saveDisabled = edit.isPending || isEmpty || isUnchanged;

  const handleSave = async (): Promise<void> => {
    if (saveDisabled) return;
    try {
      await edit.mutateAsync({ taskId, commentId: comment.id, body: trimmed });
      setEditing(false);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.comments.edit_failed'),
      });
    }
  };

  const handleDelete = async (): Promise<void> => {
    setMenuOpen(false);
    const ok = await confirmAction({ message: t('tasks.comments.delete_confirm') });
    if (!ok) return;
    try {
      await del.mutateAsync({ taskId, commentId: comment.id });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.comments.delete_failed'),
      });
    }
  };

  return (
    <Card className={styles.card}>
      <header className={styles.header}>
        <strong>{comment.authorDisplayName || t('common.deactivated_user')}</strong>
        <div className={styles.headerRight}>
          <span className={styles.timestamp}>{formatEpochDateTime(comment.createdAt, locale)}</span>
          {edited ? <span className={styles.editedHint}>{t('tasks.comments.edited')}</span> : null}
          {isAuthor && !editing ? (
            <Popover
              open={menuOpen}
              onOpenChange={setMenuOpen}
              placement="bottom-end"
              content={
                <div
                  role="menu"
                  aria-label={t('tasks.comments.menu_label')}
                  className={styles.menuList}
                >
                  <button
                    type="button"
                    role="menuitem"
                    className={styles.menuItem}
                    onClick={enterEdit}
                  >
                    {t('tasks.comments.edit')}
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    className={`${styles.menuItem} ${styles.menuItemDanger}`}
                    onClick={() => {
                      void handleDelete();
                    }}
                  >
                    {t('tasks.comments.delete')}
                  </button>
                </div>
              }
            >
              <button
                type="button"
                aria-label={t('tasks.comments.menu_label')}
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                className={styles.trigger}
              >
                <MoreHorizontal size={16} aria-hidden />
              </button>
            </Popover>
          ) : null}
        </div>
      </header>

      {editing ? (
        <div className={styles.editor}>
          {/*
           * Same editor as the new-comment box, so editing a comment can
           * name a person the same way writing one can. The Escape handler
           * only sees keys the mention picker did not take, so dismissing
           * the picker no longer throws away the edit behind it.
           */}
          <MarkdownEditor
            value={draft}
            onChange={setDraft}
            workspaceId={workspaceId}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault();
                cancelEdit();
              }
            }}
            rows={3}
            autoFocus
            aria-label={t('tasks.comments.edit')}
            placeholder={t('tasks.comments.edit_placeholder')}
          />
          <div className={styles.editorActions}>
            <Button type="button" variant="ghost" onClick={cancelEdit}>
              {t('tasks.comments.cancel')}
            </Button>
            <Button
              type="button"
              disabled={saveDisabled}
              onClick={() => {
                void handleSave();
              }}
            >
              {t('tasks.comments.save')}
            </Button>
          </div>
        </div>
      ) : (
        <Markdown>{comment.body}</Markdown>
      )}
    </Card>
  );
}
