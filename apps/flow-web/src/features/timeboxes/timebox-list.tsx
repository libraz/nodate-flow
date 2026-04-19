/**
 * TimeboxList — renders workspace timeboxes grouped by status.
 *
 * Active timeboxes appear first, followed by planned, completed, and
 * cancelled. Each card shows name, date range, status badge, and a
 * simple progress indicator. A "Create timebox" button opens the
 * create dialog.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatDateOnly } from '../../lib/format';
import { type TimeboxItem, type TimeboxStatus, useTimeboxesQuery } from './api';
import TimeboxCreateDialog from './timebox-create-dialog';
import styles from './timeboxes.module.css';

export interface TimeboxListProps {
  workspaceId: string;
}

/** Ordered status groups: active first, then planned, completed, cancelled. */
const STATUS_ORDER: readonly TimeboxStatus[] = ['active', 'planned', 'completed', 'cancelled'];

/** CSS class for a status badge. */
function badgeClass(status: TimeboxStatus): string {
  const map: Record<TimeboxStatus, string | undefined> = {
    planned: styles.badgePlanned,
    active: styles.badgeActive,
    completed: styles.badgeCompleted,
    cancelled: styles.badgeCancelled,
  };
  return cx(styles.badge, map[status]);
}

function TimeboxCard({
  item,
  locale,
}: {
  item: TimeboxItem;
  locale: string;
}): ReactElement {
  const { t } = useTranslation('timeboxes');

  return (
    <article className={styles.card}>
      <div className={styles.cardTop}>
        <h3 className={styles.cardName}>{item.name}</h3>
        <span className={badgeClass(item.status)}>{t(`status.${item.status}`)}</span>
      </div>
      <div className={styles.cardMeta}>
        <span className={styles.dateRange}>
          {formatDateOnly(item.startsOn, locale)} – {formatDateOnly(item.endsOn, locale)}
        </span>
        {item.projectName ? <span className={styles.projectName}>{item.projectName}</span> : null}
      </div>
      {item.total > 0 ? (
        <div
          className={styles.progressBar}
          role="progressbar"
          aria-valuenow={0}
          aria-valuemin={0}
          aria-valuemax={item.total}
          aria-label={t('progress.title')}
        >
          {/* Detailed progress requires task-level data; placeholder at 0% for now */}
          <div className={styles.progressFill} style={{ inlineSize: '0%' }} />
        </div>
      ) : null}
    </article>
  );
}

export default function TimeboxList({ workspaceId }: TimeboxListProps): ReactElement {
  const { t, i18n } = useTranslation('timeboxes');
  const { data: timeboxes } = useTimeboxesQuery(workspaceId);
  const [createOpen, setCreateOpen] = useState(false);

  const locale = i18n.resolvedLanguage ?? 'en';

  // Group timeboxes by status
  const grouped = new Map<TimeboxStatus, TimeboxItem[]>();
  for (const status of STATUS_ORDER) {
    grouped.set(status, []);
  }
  for (const item of timeboxes) {
    const list = grouped.get(item.status);
    if (list) {
      list.push(item);
    }
  }

  const hasAny = timeboxes.length > 0;

  return (
    <section className={styles.container}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('title')}</h1>
        <Button
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('create')}
        </Button>
      </header>

      {hasAny ? (
        STATUS_ORDER.map((status) => {
          const items = grouped.get(status);
          if (!items || items.length === 0) return null;
          return (
            <section key={status} className={styles.statusGroup}>
              <h2 className={styles.statusGroupTitle}>{t(`status.${status}`)}</h2>
              {items.map((item) => (
                <TimeboxCard key={item.id} item={item} locale={locale} />
              ))}
            </section>
          );
        })
      ) : (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>{t('empty')}</p>
          <p className={styles.emptyDescription}>{t('empty_description')}</p>
        </div>
      )}

      <TimeboxCreateDialog
        workspaceId={workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </section>
  );
}
