/**
 * RemindersPage — `/workspaces/{wsId}/reminders`. Surfaces every
 * reminder produced by the deterministic reminder engine
 * (`GET /workspaces/{wsId}/ai/reminders`), grouped by urgency:
 *
 *   - **Overdue** (`daysUntilDue < 0`)        → tone: danger
 *   - **Today**   (`daysUntilDue === 0`)       → tone: warning
 *   - **Soon**    (`daysUntilDue` in [1, 3])   → tone: info
 *   - **Later**   (`daysUntilDue > 3`)         → tone: neutral
 *
 * The reminder engine itself runs server-side and reflects derived
 * state (overdue / due_today / due_soon) plus the message string. This
 * page is read-only.
 *
 * **Snooze / dismiss are intentionally absent.** No such endpoints
 * exist on flow-api today — the only reminder endpoint is the GET
 * above. Adding buttons would 404. If reminder actions are required
 * later they need backend work first.
 *
 * Data fetching uses the existing non-suspense `useRemindersQuery`
 * (60s polling, tolerant of failures) so the dock + this page share a
 * cache. On query error we render an inline retry surface instead of
 * throwing — same posture as the dock.
 *
 * Hooks consumed (in `./api.ts`):
 *   - {@link useRemindersQuery} — list reminders (poll-friendly useQuery)
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { Link, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { type TaskReminder, useRemindersQuery } from './api';
import styles from './reminders-page.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/reminders');

type BucketId = 'overdue' | 'today' | 'soon' | 'later';

interface Bucket {
  id: BucketId;
  tone: BadgeTone;
  items: TaskReminder[];
}

/** Classify a reminder by `daysUntilDue` into one of four urgency buckets. */
function bucketFor(daysUntilDue: number): BucketId {
  if (daysUntilDue < 0) return 'overdue';
  if (daysUntilDue === 0) return 'today';
  if (daysUntilDue <= 3) return 'soon';
  return 'later';
}

/** Group reminders into ordered buckets, dropping empty ones. */
function groupReminders(reminders: readonly TaskReminder[]): Bucket[] {
  const buckets: Record<BucketId, TaskReminder[]> = {
    overdue: [],
    today: [],
    soon: [],
    later: [],
  };
  for (const r of reminders) {
    buckets[bucketFor(r.daysUntilDue)].push(r);
  }
  // Within each bucket, sort overdue most-overdue-first; future buckets
  // soonest-first. Stable on title as a tiebreaker.
  const byUrgency = (a: TaskReminder, b: TaskReminder): number => {
    if (a.daysUntilDue !== b.daysUntilDue) return a.daysUntilDue - b.daysUntilDue;
    return a.title.localeCompare(b.title);
  };
  const ordered: Array<{ id: BucketId; tone: BadgeTone }> = [
    { id: 'overdue', tone: 'danger' },
    { id: 'today', tone: 'warning' },
    { id: 'soon', tone: 'info' },
    { id: 'later', tone: 'neutral' },
  ];
  return ordered
    .map(({ id, tone }) => ({ id, tone, items: [...buckets[id]].sort(byUrgency) }))
    .filter((b) => b.items.length > 0);
}

/** Format a YYYY-MM-DD due date using the active locale's medium date style. */
function formatDueOn(dueOn: string, locale: string): string {
  if (!dueOn) return '';
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(
      new Date(`${dueOn}T00:00:00`),
    );
  } catch {
    return dueOn;
  }
}

interface ReminderRowProps {
  reminder: TaskReminder;
  locale: string;
}

function ReminderRow({ reminder, locale }: ReminderRowProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const days = reminder.daysUntilDue;
  let daysText: string;
  if (days < 0) {
    daysText = t('reminders.days.overdue', { count: Math.abs(days) });
  } else if (days === 0) {
    daysText = t('reminders.days.today');
  } else {
    daysText = t('reminders.days.future', { count: days });
  }
  const dueDate = formatDueOn(reminder.dueOn, locale);
  return (
    <li className={styles.row}>
      <div className={styles.rowMain}>
        <div className={styles.rowHeader}>
          <Badge tone="accent">
            {t(`reminders.kind.${reminder.kind}`, { defaultValue: reminder.kind })}
          </Badge>
          <Link
            to="/tasks/$taskId"
            params={{ taskId: reminder.taskId }}
            className={styles.rowTitle}
          >
            {reminder.title}
          </Link>
        </div>
        {reminder.message ? <p className={styles.rowMessage}>{reminder.message}</p> : null}
      </div>
      <div className={styles.rowMeta}>
        <span>{daysText}</span>
        {dueDate ? <span>{t('reminders.due', { date: dueDate })}</span> : null}
      </div>
    </li>
  );
}

interface BucketSectionProps {
  bucket: Bucket;
  locale: string;
}

function BucketSection({ bucket, locale }: BucketSectionProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  return (
    <section className={styles.group} aria-label={t(`reminders.bucket.${bucket.id}`)}>
      <header className={styles.groupHeader}>
        <h2 className={styles.groupTitle}>
          <Badge tone={bucket.tone}>{t(`reminders.bucket.${bucket.id}`)}</Badge>
        </h2>
        <span className={styles.groupCount}>{bucket.items.length}</span>
      </header>
      <ul className={styles.list}>
        {bucket.items.map((r) => (
          <ReminderRow key={r.taskId} reminder={r} locale={locale} />
        ))}
      </ul>
    </section>
  );
}

/** Page component mounted by the lazy route. */
export default function RemindersPage(): ReactElement {
  const { t, i18n } = useTranslation('ai-suggestions');
  const { id: workspaceId } = routeApi.useParams();
  const locale = i18n.resolvedLanguage ?? 'en';
  const remindersQuery = useRemindersQuery(workspaceId);
  const reminders = remindersQuery.data ?? [];
  const buckets = useMemo(() => groupReminders(reminders), [reminders]);
  const total = reminders.length;

  return (
    <section className={styles.page}>
      <div className={styles.header}>
        <h1 className={styles.title}>{t('reminders.page_title')}</h1>
        <p className={styles.subtitle}>{t('reminders.page_subtitle')}</p>
        <p className={styles.count}>{t('reminders.count', { count: total })}</p>
      </div>
      {remindersQuery.isError ? (
        <Card>
          <div className={styles.errorBox}>
            <p className={styles.errorMessage}>{t('reminders.error')}</p>
            <Button
              type="button"
              variant="default"
              size="sm"
              onClick={() => {
                void remindersQuery.refetch();
              }}
            >
              {t('reminders.retry')}
            </Button>
          </div>
        </Card>
      ) : buckets.length === 0 ? (
        <p className={styles.empty}>{t('reminders.empty')}</p>
      ) : (
        <div className={styles.groups}>
          {buckets.map((bucket) => (
            <BucketSection key={bucket.id} bucket={bucket} locale={locale} />
          ))}
        </div>
      )}
    </section>
  );
}
