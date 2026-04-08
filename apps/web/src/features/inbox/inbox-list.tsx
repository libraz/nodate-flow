/**
 * InboxList — renders the caller's inbox items via `useInboxQuery` and wires
 * archive / snooze mutations through to row callbacks. Empty state is themed.
 */

import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useArchiveInboxItem, useInboxQuery, useSnoozeInboxItem } from './api';
import InboxItemRow from './inbox-item-row';

export default function InboxList(): ReactElement {
  const { t } = useTranslation('inbox');
  const { data: items } = useInboxQuery();
  const archive = useArchiveInboxItem();
  const snooze = useSnoozeInboxItem();

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

  if (items.length === 0) {
    return (
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
    );
  }

  return (
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
      {items.map((item) => (
        <li key={item.id}>
          <InboxItemRow item={item} onArchive={handleArchive} onSnooze={handleSnooze} />
        </li>
      ))}
    </ul>
  );
}
