/**
 * ShareDetail — editor page for a single public share. Lists attached events,
 * lets the workspace admin detach individual events, and opens a picker to
 * add new ones from the workspace's calendar events.
 *
 * Data:
 *   - `GET /workspaces/{wsId}/public-shares/{shareId}` returns share metadata
 *     plus the already-attached events (editor projection).
 *   - `GET /workspaces/{wsId}/calendar-events?start=&end=` lists candidates for
 *     the picker; confidential events are filtered client-side and also
 *     rejected server-side at attach time.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link } from '@tanstack/react-router';
import { ArrowLeft, Plus } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import AddEventsDialog from './add-events-dialog';
import { type ShareEvent, useDetachEventFromShare, usePublicShareDetailQuery } from './api';

export interface ShareDetailProps {
  workspaceId: string;
  shareId: string;
}

/** Format a start/end pair into a compact localised range label. */
function formatWhen(event: ShareEvent, locale: string, allDayLabel: string): string {
  if (!event.startAt) return '—';
  const start = new Date(event.startAt * 1000);
  const end = event.endAt ? new Date(event.endAt * 1000) : null;
  const dateFmt = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' });
  const timeFmt = new Intl.DateTimeFormat(locale, { timeStyle: 'short' });
  if (event.allDay) {
    if (!end || sameDay(start, end)) return `${dateFmt.format(start)} · ${allDayLabel}`;
    return `${dateFmt.format(start)} – ${dateFmt.format(end)}`;
  }
  if (!end) return `${dateFmt.format(start)} ${timeFmt.format(start)}`;
  if (sameDay(start, end)) {
    return `${dateFmt.format(start)} ${timeFmt.format(start)}–${timeFmt.format(end)}`;
  }
  return `${dateFmt.format(start)} ${timeFmt.format(start)} – ${dateFmt.format(end)} ${timeFmt.format(end)}`;
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

export default function ShareDetail({ workspaceId, shareId }: ShareDetailProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const locale = i18n.resolvedLanguage ?? 'en';
  const { data } = usePublicShareDetailQuery(workspaceId, shareId);
  const detach = useDetachEventFromShare(workspaceId, shareId);
  const [pickerOpen, setPickerOpen] = useState(false);

  const handleDetach = async (event: ShareEvent): Promise<void> => {
    if (
      !(await confirmAction({
        message: t('workspace.public_shares.detail.detach_confirm', { title: event.title }),
      }))
    ) {
      return;
    }
    try {
      await detach.mutateAsync(event.eventId);
      toaster.show({ tone: 'success', message: t('workspace.public_shares.detail.detached') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.public_shares.detail.errors.detach_failed'),
      });
    }
  };

  const allDayLabel = t('workspace.public_shares.detail.event_all_day');
  const attachedIds = new Set(data.events?.map((e) => e.eventId) ?? []);

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <div>
        <Link
          to="/workspaces/$id/settings/public-shares"
          params={{ id: workspaceId }}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.25rem',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.8125rem',
            textDecoration: 'none',
          }}
        >
          <ArrowLeft size={14} aria-hidden />
          {t('workspace.public_shares.detail.back')}
        </Link>
      </div>

      <header
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '1rem',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{data.share.title}</h1>
          {data.share.description ? (
            <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
              {data.share.description}
            </p>
          ) : null}
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setPickerOpen(true);
          }}
        >
          <Plus size={14} aria-hidden style={{ marginInlineEnd: '0.25rem' }} />
          {t('workspace.public_shares.detail.add_events')}
        </Button>
      </header>

      <div>
        <h2 style={{ margin: '0 0 0.25rem', fontSize: '1rem' }}>
          {t('workspace.public_shares.detail.events_title')}
        </h2>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.8125rem' }}>
          {t('workspace.public_shares.detail.events_description')}
        </p>
      </div>

      {data.events && data.events.length > 0 ? (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ inlineSize: '100%', borderCollapse: 'collapse', fontSize: '0.875rem' }}>
            <thead>
              <tr style={{ textAlign: 'start', color: 'var(--nf-color-fg-muted)' }}>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.detail.table.event')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.detail.table.when')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.public_shares.detail.table.calendar')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'end' }}>
                  {t('workspace.public_shares.detail.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {data.events.map((event) => (
                <tr
                  key={event.linkId}
                  style={{ borderBlockStart: '1px solid var(--nf-color-border)' }}
                >
                  <td style={{ padding: '0.75rem' }}>
                    <div style={{ fontWeight: 500 }}>{event.title}</div>
                    {event.location ? (
                      <div style={{ fontSize: '0.75rem', color: 'var(--nf-color-fg-muted)' }}>
                        {event.location}
                      </div>
                    ) : null}
                  </td>
                  <td style={{ padding: '0.75rem' }}>
                    {event.startAt
                      ? formatWhen(event, locale, allDayLabel)
                      : t('workspace.public_shares.detail.event_undated')}
                  </td>
                  <td style={{ padding: '0.75rem' }}>{event.calendarName}</td>
                  <td
                    style={{
                      padding: '0.75rem',
                      textAlign: 'end',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => {
                        void handleDetach(event);
                      }}
                    >
                      {t('workspace.public_shares.detail.detach')}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspace.public_shares.detail.empty_events')}
        </p>
      )}

      <Suspense
        fallback={
          <div style={{ display: 'none' }}>
            <Skeleton style={{ blockSize: '1px' }} />
          </div>
        }
      >
        <AddEventsDialog
          workspaceId={workspaceId}
          shareId={shareId}
          open={pickerOpen}
          attachedIds={attachedIds}
          onClose={() => {
            setPickerOpen(false);
          }}
        />
      </Suspense>
    </section>
  );
}
