/**
 * IntakeList — workspace-scoped triage queue rendered as the second tab
 * of `/inbox`. The user picks a workspace, types a quick-add line, and
 * each pending row exposes Convert / Dismiss actions.
 *
 * The component owns its workspace selection independently from the
 * Inbox tab because the two views serve different concerns: Inbox is a
 * cross-workspace signal river, intake is per-workspace queue
 * management. Tab switches preserve each tab's local state through the
 * route-level Tabs primitive.
 *
 * Convert opens {@link IntakeConvertDialog} with the chosen item; the
 * dialog handles the project picker and the navigate-to-task hop.
 * Dismiss flips the row to `rejected` so it disappears from the
 * pending list immediately.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type FormEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../../workspaces/api';
import {
  type IntakeItem,
  useCreateIntakeItemMutation,
  useIntakeQuery,
  useTriageIntakeItemMutation,
} from './api';
import IntakeConvertDialog from './intake-convert-dialog';
import styles from './intake-list.module.css';

export default function IntakeList(): ReactElement {
  const { t } = useTranslation('inbox');
  const { data: workspaces } = useWorkspacesQuery();
  const [workspaceId, setWorkspaceId] = useState<string>(workspaces[0]?.id ?? '');

  const handleWorkspaceChange = (event: ChangeEvent<HTMLSelectElement>): void => {
    setWorkspaceId(event.target.value);
  };

  if (workspaces.length === 0) {
    return <p className={styles.empty}>{t('intake.no_workspace')}</p>;
  }

  return (
    <div className={styles.body}>
      <div className={styles.toolbar}>
        <label className={styles.workspaceField} htmlFor="intake-workspace-select">
          {t('filter.workspace_label')}
          <Select
            id="intake-workspace-select"
            value={workspaceId}
            onChange={handleWorkspaceChange}
            aria-label={t('filter.workspace_label')}
          >
            {workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </Select>
        </label>
      </div>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <IntakeBody workspaceId={workspaceId} />
      </Suspense>
    </div>
  );
}

interface IntakeBodyProps {
  workspaceId: string;
}

function IntakeBody({ workspaceId }: IntakeBodyProps): ReactElement {
  const { t } = useTranslation('inbox');
  const { data: items } = useIntakeQuery(workspaceId, 'pending');
  const create = useCreateIntakeItemMutation();
  const triage = useTriageIntakeItemMutation();

  const [title, setTitle] = useState('');
  const [convertItem, setConvertItem] = useState<IntakeItem | null>(null);

  const handleQuickAdd = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const trimmed = title.trim();
    if (!trimmed) return;
    create.mutate(
      { wsId: workspaceId, title: trimmed },
      {
        onSuccess: () => {
          setTitle('');
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('intake.quick_add.error') });
        },
      },
    );
  };

  const handleDismiss = (item: IntakeItem): void => {
    triage.mutate(
      { wsId: workspaceId, id: item.id, status: 'rejected' },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('intake.dismiss.success') });
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('intake.dismiss.error') });
        },
      },
    );
  };

  return (
    <>
      <form className={styles.quickAdd} onSubmit={handleQuickAdd}>
        <Input
          className={styles.quickAddInput}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t('intake.quick_add.placeholder')}
          aria-label={t('intake.quick_add.placeholder')}
          disabled={create.isPending}
          maxLength={500}
        />
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={create.isPending || title.trim().length === 0}
        >
          {t('intake.quick_add.submit')}
        </Button>
      </form>

      {items.length === 0 ? (
        <p className={styles.empty}>{t('intake.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {items.map((item) => (
            <li key={item.id} className={styles.row}>
              <div className={styles.rowMain}>
                <p className={styles.rowTitle}>{item.title}</p>
                {item.body ? <p className={styles.rowBody}>{item.body}</p> : null}
              </div>
              <div className={styles.rowActions}>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => handleDismiss(item)}
                  disabled={triage.isPending}
                >
                  {t('intake.dismiss.action')}
                </Button>
                <Button
                  type="button"
                  variant="primary"
                  size="sm"
                  onClick={() => setConvertItem(item)}
                >
                  {t('intake.convert.action')}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <IntakeConvertDialog
        workspaceId={workspaceId}
        item={convertItem}
        open={convertItem !== null}
        onClose={() => setConvertItem(null)}
      />
    </>
  );
}
