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
 * cache. While the first fetch is pending the page shows row-shaped
 * skeletons rather than the empty state; on query error it renders an
 * inline retry surface instead of throwing — same posture as the dock.
 *
 * Hooks consumed (in `./api.ts`):
 *   - {@link useRemindersQuery} — list reminders (poll-friendly useQuery)
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { getRouteApi, Link } from '@tanstack/react-router';
import { type CSSProperties, type ReactElement, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { type TaskReminder, useRemindersQuery } from './api';
import styles from './reminders-page.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/reminders');

type BucketId = 'overdue' | 'today' | 'soon' | 'later';

const BUCKET_KEY: Record<BucketId, string> = {
  overdue: 'reminders.bucket.overdue',
  today: 'reminders.bucket.today',
  soon: 'reminders.bucket.soon',
  later: 'reminders.bucket.later',
};

/** Engine kinds with a label; an unknown kind shows its raw name. */
const KIND_KEY = new Map<string, string>([
  ['overdue', 'reminders.kind.overdue'],
  ['due_today', 'reminders.kind.due_today'],
  ['due_soon', 'reminders.kind.due_soon'],
]);

const SKELETON_ROWS = ['a', 'b', 'c'] as const;

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
  style: CSSProperties | undefined;
}

function ReminderRow({ reminder, locale, style }: ReminderRowProps): ReactElement {
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
  const kindKey = KIND_KEY.get(reminder.kind);
  return (
    <li className={cx(styles.row, motion.enter)} style={style}>
      <div className={styles.rowMain}>
        <div className={styles.rowHeader}>
          <Badge tone="accent">{kindKey ? t(kindKey) : reminder.kind}</Badge>
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
  /** Page-wide index of the bucket's first row, so the stagger runs down the page. */
  offset: number;
  staggerFor: (index: number) => CSSProperties | undefined;
}

function BucketSection({ bucket, locale, offset, staggerFor }: BucketSectionProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  return (
    <section className={styles.group} aria-label={t(BUCKET_KEY[bucket.id])}>
      <header className={styles.groupHeader}>
        <h2 className={styles.groupTitle}>
          <Badge tone={bucket.tone}>{t(BUCKET_KEY[bucket.id])}</Badge>
        </h2>
        <span className={styles.groupCount}>{bucket.items.length}</span>
      </header>
      <ul className={styles.list}>
        {bucket.items.map((r, i) => (
          <ReminderRow key={r.taskId} reminder={r} locale={locale} style={staggerFor(offset + i)} />
        ))}
      </ul>
    </section>
  );
}

/** Mounted once data is in, so the first-load stagger starts with the rows. */
function ReminderGroups({
  buckets,
  locale,
}: {
  buckets: readonly Bucket[];
  locale: string;
}): ReactElement {
  const staggerFor = useFirstLoadStagger();
  let offset = 0;
  return (
    <div className={styles.groups}>
      {buckets.map((bucket) => {
        const start = offset;
        offset += bucket.items.length;
        return (
          <BucketSection
            key={bucket.id}
            bucket={bucket}
            locale={locale}
            offset={start}
            staggerFor={staggerFor}
          />
        );
      })}
    </div>
  );
}

function RemindersSkeleton(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <ul className={styles.list} aria-busy="true" aria-label={t('common.loading')}>
      {SKELETON_ROWS.map((id) => (
        <li key={id} className={styles.row}>
          <div className={styles.rowMain}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '1.25rem', inlineSize: '55%' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '0.875rem', inlineSize: '35%' }} />
          </div>
        </li>
      ))}
    </ul>
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
        <p className={styles.count}>
          {remindersQuery.isPending ? null : t('reminders.count', { count: total })}
        </p>
      </div>
      {remindersQuery.isPending ? (
        <RemindersSkeleton />
      ) : remindersQuery.isError ? (
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
        <EmptyState className={styles.empty} titleAs="p" title={t('reminders.empty')} />
      ) : (
        <ReminderGroups buckets={buckets} locale={locale} />
      )}
    </section>
  );
}
