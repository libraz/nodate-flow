/**
 * The day sheet is the only place the phone calendar can be operated
 * from, so everything a reader of the month grid cannot reach — the
 * chips there are drawn, not pressed — has to be reachable here. This
 * checks the structure the sheet ships: a labelled dialog, rows that are
 * real controls, and no violation axe can name.
 *
 * The sheet portals out of the tree it is rendered into, so the audit
 * runs against the document rather than the render container.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { resolvedLanguage: 'en', language: 'en' },
  }),
}));

import DayDetailSheet from '../day-detail-sheet';

type CalendarEvent = components['schemas']['MyCalendarEventResponse'];
type CalendarTask = components['schemas']['MyTaskListItem'];

const DAY = '2026-04-01';

function event(id: string, title: string, hour: number, allDay = false): CalendarEvent {
  const startAt = Math.floor(Date.parse(`${DAY}T${String(hour).padStart(2, '0')}:00:00Z`) / 1000);
  return {
    allDay,
    attendeeCount: 0,
    calendarId: 'cal-1',
    createdAt: 0,
    endAt: startAt + 3600,
    flexibility: 'fixed',
    id,
    kind: 'event',
    ownerUserId: 'u1',
    showAs: 'busy',
    startAt,
    timezone: 'UTC',
    title,
    viewerAttending: false,
    visibility: 'default',
    workspaceId: 'ws-1',
    workspaceName: 'WS',
  } as CalendarEvent;
}

function task(id: string, title: string): CalendarTask {
  return {
    actorRole: 'owner',
    createdAt: 0,
    derivedState: 'active',
    dueOn: DAY,
    id,
    priority: 1,
    projectId: 'p1',
    title,
    workspaceId: 'ws-1',
    workspaceName: 'WS',
  } as CalendarTask;
}

function renderSheet(): void {
  const noop = (): void => {};
  render(
    <DayDetailSheet
      dateKey={DAY}
      locale="en"
      zone={Zone.utc()}
      events={[event('e0', 'Company holiday', 0, true), event('e1', 'Standup', 9)]}
      tasks={[task('t1', 'Ship the report')]}
      holidays={[
        { date: DAY, name: 'Founding Day', localNames: { en: 'Founding Day' }, type: 'public' },
      ]}
      stateColor={() => 'var(--nf-color-fg-muted)'}
      onClose={noop}
      onEventOpen={noop}
      onTaskOpen={noop}
      onCreate={noop}
    />,
  );
}

describe('DayDetailSheet a11y', () => {
  it('has no axe violations', async () => {
    renderSheet();
    expect(await axe(document.body)).toHaveNoViolations();
  });

  it('names the day and the holiday it falls on in the dialog label', () => {
    renderSheet();
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    // The header is the only thing that says which day was opened; the
    // rows below it carry times, not dates.
    expect(within(dialog).getByText('Wednesday, April 1, 2026')).toBeTruthy();
    expect(within(dialog).getByText('Founding Day')).toBeTruthy();
  });

  it('makes every row and the create action a real control', () => {
    renderSheet();
    const labels = within(screen.getByRole('dialog'))
      .getAllByRole('button')
      .map((el) => el.textContent ?? '');
    expect(labels).toHaveLength(4);
    expect(labels[3]).toContain('calendar.day_detail.create');
  });
});
