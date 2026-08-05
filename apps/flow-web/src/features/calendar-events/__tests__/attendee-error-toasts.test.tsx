/**
 * Attendee failure messaging.
 *
 * Both error paths used to pass a button label to the toast — adding an
 * attendee failed and the red toast said "Add", issuing an invite link
 * failed and it said "Send invite link". Neither told the reader what
 * went wrong or whether retrying was worth it, and both discarded the
 * error object entirely, so the server's reason never reached the UI at
 * all.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { Suspense } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  addAttendees: vi.fn(),
  createInvite: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('../attendees-api', () => ({
  useAttendeesQuery: () => ({
    data: [
      {
        id: 'att-1',
        userId: 'user-2',
        displayName: 'Rin',
        rsvp: 'pending',
        canEdit: false,
      },
    ],
    isLoading: false,
  }),
  useAddAttendeesMutation: () => ({ mutate: mocks.addAttendees, isPending: false }),
  useRemoveAttendeeMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateOwnRsvpMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useToggleCanEditMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateAttendeeInviteMutation: () => ({ mutateAsync: mocks.createInvite, isPending: false }),
}));

vi.mock('../../workspaces/api', () => ({
  useWorkspaceMembersQuery: () => ({
    data: [{ userId: 'user-3', displayName: 'Kai' }],
    isLoading: false,
  }),
}));

import { ApiError } from '../../../lib/api-error';
import AttendeesSection from '../attendees-section';

/**
 * A real bundle, because the assertion is that a translated message is
 * what reaches the toast. With key passthrough the old code and the new
 * code would be indistinguishable.
 */
function buildI18n(): ReturnType<typeof i18next.createInstance> {
  const instance = i18next.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'calendar-events',
      ns: ['calendar-events', 'errors'],
      resources: {
        en: {
          'calendar-events': {
            event: {
              attendees: {
                title: 'Attendees',
                add: 'Add',
                send_invite: 'Send invite link',
                placeholder: 'Add a workspace member...',
                remove: 'Remove attendee',
                empty: 'No attendees yet',
                invite_sent: 'Invite link copied',
                you: 'you',
                can_edit: 'Can edit',
                your_response: 'Your response',
                rsvp: {
                  accepted: 'Going',
                  declined: 'Declined',
                  pending: 'Pending',
                  tentative: 'Maybe',
                },
                errors: {
                  add_failed: 'Could not add the attendee',
                  invite_failed: 'Could not create the invite link',
                },
              },
            },
          },
          errors: { 'CALENDAR.ATTENDEE.LIMIT_REACHED': 'This event is full' },
        },
      },
      interpolation: { escapeValue: false },
      parseMissingKeyHandler: (key: string, defaultValue?: string) =>
        defaultValue !== undefined ? defaultValue : key,
      react: { useSuspense: false },
    });
  return instance;
}

function renderSection(): void {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, throwOnError: false },
      mutations: { retry: false, throwOnError: false },
    },
  });
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return (
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={buildI18n()}>
          <Suspense fallback={null}>{children}</Suspense>
        </I18nextProvider>
      </QueryClientProvider>
    );
  }
  render(
    <Wrapper>
      <AttendeesSection
        workspaceId="ws-1"
        calendarId="cal-1"
        eventId="evt-1"
        ownerUserId="user-1"
        selfUserId="user-1"
      />
    </Wrapper>,
  );
}

beforeEach(() => {
  mocks.addAttendees.mockReset();
  mocks.createInvite.mockReset();
  mocks.toastShow.mockReset();
});

describe('AttendeesSection failure messaging', () => {
  it('explains a failed add instead of echoing the button label', async () => {
    const user = userEvent.setup();
    mocks.addAttendees.mockImplementation(
      (_vars: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError(new Error('boom'));
      },
    );
    renderSection();

    await user.click(screen.getByRole('combobox', { name: 'Add' }));
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeDefined();
    });
    await user.click(screen.getByRole('option', { name: 'Kai' }));

    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('danger');
    expect(toast.message).toBe('boom');
    expect(toast.message).not.toBe('Add');
  });

  it('surfaces the server reason for a failed add when there is a code', async () => {
    const user = userEvent.setup();
    mocks.addAttendees.mockImplementation(
      (_vars: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError(
          new ApiError('CALENDAR.ATTENDEE.LIMIT_REACHED', 'attendee limit reached', 422),
        );
      },
    );
    renderSection();

    await user.click(screen.getByRole('combobox', { name: 'Add' }));
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeDefined();
    });
    await user.click(screen.getByRole('option', { name: 'Kai' }));

    const toast = mocks.toastShow.mock.calls[0]?.[0] as { message: string };
    expect(toast.message).toBe('This event is full');
  });

  it('falls back to the attendee-specific copy when the failure carries nothing usable', async () => {
    const user = userEvent.setup();
    mocks.addAttendees.mockImplementation(
      (_vars: unknown, opts: { onError: (e: unknown) => void }) => {
        opts.onError('not an error object');
      },
    );
    renderSection();

    await user.click(screen.getByRole('combobox', { name: 'Add' }));
    await waitFor(() => {
      expect(screen.getByRole('listbox')).toBeDefined();
    });
    await user.click(screen.getByRole('option', { name: 'Kai' }));

    const toast = mocks.toastShow.mock.calls[0]?.[0] as { message: string };
    expect(toast.message).toBe('Could not add the attendee');
  });

  it('explains a failed invite instead of echoing the button label', async () => {
    const user = userEvent.setup();
    mocks.createInvite.mockRejectedValue('nope');
    renderSection();

    await user.click(screen.getByRole('button', { name: 'Send invite link' }));

    await waitFor(() => {
      expect(mocks.toastShow).toHaveBeenCalled();
    });
    const toast = mocks.toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(toast.tone).toBe('danger');
    expect(toast.message).toBe('Could not create the invite link');
    expect(toast.message).not.toBe('Send invite link');
  });
});
