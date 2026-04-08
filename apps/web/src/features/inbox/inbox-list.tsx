/**
 * InboxList — renders the caller's inbox items via `useInboxQuery` and wires
 * archive / snooze mutations through to row callbacks. Includes a workspace
 * filter select that narrows the visible items client-side.
 */

import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspacesQuery } from '../workspaces/api';
import { useArchiveInboxItem, useInboxQuery, useSnoozeInboxItem } from './api';
import InboxItemRow from './inbox-item-row';

type WorkspaceFilter = string | 'all';

export default function InboxList(): ReactElement {
  const { t } = useTranslation('inbox');
  const { data: items } = useInboxQuery();
  const { data: workspaces } = useWorkspacesQuery();
  const archive = useArchiveInboxItem();
  const snooze = useSnoozeInboxItem();
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<WorkspaceFilter>('all');

  const handleArchive = (id: string): void => {
    archive.mutate(id, {
      onSuccess: () => {
        toaster.show({ tone: 'success', message: t('toast.archived') });
      },
      onError: () => {
        toaster.show({ tone: 'danger', message: t('toast.error') });
      },
    });
  };

  const handleSnooze = (id: string, snoozeUntil: number): void => {
    snooze.mutate(
      { id, snoozeUntil },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('toast.snoozed') });
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('toast.error') });
        },
      },
    );
  };

  const handleWorkspaceChange = (event: ChangeEvent<HTMLSelectElement>): void => {
    setSelectedWorkspaceId(event.target.value as WorkspaceFilter);
  };

  const filteredItems =
    selectedWorkspaceId === 'all'
      ? items
      : items.filter((item) => item.workspaceId === selectedWorkspaceId);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <label
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: '0.375rem',
          fontSize: '0.875rem',
          color: 'var(--color-muted)',
        }}
      >
        {t('filter.workspace_label')}
        <select
          value={selectedWorkspaceId}
          onChange={handleWorkspaceChange}
          style={{
            padding: '0.5rem 0.75rem',
            borderRadius: '0.5rem',
            border: '1px solid var(--color-border)',
            background: 'var(--color-surface)',
            color: 'var(--color-fg)',
            fontSize: '0.9375rem',
            maxInlineSize: '20rem',
          }}
        >
          <option value="all">{t('filter.workspace_all')}</option>
          {workspaces.map((workspace) => (
            <option key={workspace.id} value={workspace.id}>
              {workspace.name}
            </option>
          ))}
        </select>
      </label>

      {filteredItems.length === 0 ? (
        <p
          style={{
            color: 'var(--color-muted)',
            margin: 0,
            padding: '2rem',
            textAlign: 'center',
          }}
        >
          {t('view.empty')}
        </p>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.75rem',
          }}
        >
          {filteredItems.map((item) => (
            <li key={item.id}>
              <InboxItemRow item={item} onArchive={handleArchive} onSnooze={handleSnooze} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
