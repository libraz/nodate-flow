/**
 * Member colour circle: who gets a button, saving, rollback, and the caches
 * a successful save refreshes.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  put: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('../../../lib/api', () => ({
  apiRequest: (call: (client: unknown) => Promise<unknown>) =>
    call({ PUT: mocks.put }).then((res) => {
      const r = res as { data?: unknown; error?: unknown };
      if (r.error) throw r.error;
      return r.data;
    }),
}));

import { calendarEventKeys, eventDetailKeys } from '../../events/api';
import MemberColorPopover from '../member-color-popover';
import { type CalendarMember, calendarMembersKeys } from '../members-api';

const WS = 'ws-1';
const CAL = 'cal-1';

function member(userId: string, displayName: string, memberColor: string): CalendarMember {
  return {
    id: `m-${userId}`,
    userId,
    displayName,
    memberColor,
    role: 'editor',
    createdAt: 0,
  };
}

const ana = member('u-1', 'Ana', '#47B2F7');
const ben = member('u-2', 'Ben', '#F35F8C');
const members = [ana, ben];

function buildI18n(): ReturnType<typeof i18next.createInstance> {
  const instance = i18next.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      defaultNS: 'common',
      ns: ['common'],
      resources: {
        en: {
          common: {
            calendar: {
              settings: {
                members: {
                  color_change: 'Change color for {name}',
                  color_static: 'Color of {name}',
                  color_custom: 'Custom (#RRGGBB)',
                  color_in_use: 'Used by {names}',
                  color_error: 'The color could not be changed.',
                },
              },
              labels: {
                swatch: 'Color {hex}',
                custom_color_invalid: 'Enter a color as #RRGGBB.',
              },
            },
          },
        },
      },
    });
  return instance;
}

function setup(target: CalendarMember, canChange: boolean): { qc: QueryClient } {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(calendarMembersKeys.list(WS, CAL), members);
  const ui: ReactElement = (
    <I18nextProvider i18n={buildI18n()}>
      <QueryClientProvider client={qc}>
        <MemberColorPopover
          workspaceId={WS}
          calendarId={CAL}
          member={target}
          members={members}
          canChange={canChange}
        />
      </QueryClientProvider>
    </I18nextProvider>
  );
  render(ui);
  return { qc };
}

describe('MemberColorPopover', () => {
  beforeEach(() => {
    mocks.put.mockReset();
    mocks.toastShow.mockReset();
  });

  it('renders a button when the viewer may change the colour', () => {
    setup(ana, true);
    expect(screen.getByRole('button', { name: 'Change color for Ana' })).toBeTruthy();
  });

  it('renders a named static circle otherwise', () => {
    setup(ben, false);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByRole('img', { name: 'Color of Ben' })).toBeTruthy();
  });

  it('saves the chosen colour and refreshes every event cache', async () => {
    mocks.put.mockResolvedValue({ data: { ...ana, memberColor: '#2ECC87' } });
    const { qc } = setup(ana, true);
    const spy = vi.spyOn(qc, 'invalidateQueries');
    await userEvent.click(screen.getByRole('button', { name: 'Change color for Ana' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Color #2ECC87' }));

    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(1));
    expect(mocks.put).toHaveBeenCalledWith(
      '/workspaces/{wsId}/calendars/{calId}/members/{userId}/color',
      { params: { path: { wsId: WS, calId: CAL, userId: 'u-1' } }, body: { color: '#2ECC87' } },
    );
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(4));
    const keys = spy.mock.calls.map((c) => c[0]?.queryKey);
    expect(keys).toContainEqual(calendarMembersKeys.list(WS, CAL));
    expect(keys).toContainEqual(calendarEventKeys.all);
    expect(keys).toContainEqual(calendarEventKeys.meEvents);
    expect(keys).toContainEqual(eventDetailKeys.all);
  });

  it('rolls the list back and toasts when saving fails', async () => {
    mocks.put.mockResolvedValue({ error: { status: 500, title: 'boom' } });
    const { qc } = setup(ana, true);
    await userEvent.click(screen.getByRole('button', { name: 'Change color for Ana' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Color #2ECC87' }));

    await waitFor(() => expect(mocks.toastShow).toHaveBeenCalled());
    expect(mocks.toastShow.mock.calls[0]?.[0]).toMatchObject({
      tone: 'danger',
      message: 'The color could not be changed.',
    });
    const list = qc.getQueryData<CalendarMember[]>(calendarMembersKeys.list(WS, CAL));
    expect(list?.find((m) => m.userId === 'u-1')?.memberColor).toBe('#47B2F7');
  });

  it('flags an invalid custom colour and does not save', async () => {
    setup(ana, true);
    await userEvent.click(screen.getByRole('button', { name: 'Change color for Ana' }));
    await userEvent.type(await screen.findByLabelText('Custom (#RRGGBB)'), '#12');
    expect(await screen.findByText('Enter a color as #RRGGBB.')).toBeTruthy();
    expect(mocks.put).not.toHaveBeenCalled();
  });
});
