/**
 * The way out of a save that never comes back.
 *
 * A write leaves the browser with no timeout and no `AbortSignal` — the
 * SDK sets neither — so "in flight" is a state with no upper bound. If
 * the dialog locks its cancel for the duration, an unreachable server
 * seals the user inside a modal with no exit and nothing on screen to
 * distinguish a slow save from a frozen page.
 *
 * These tests hold the escape open. The mutation mocks here are
 * deliberately not the `isPending: false` constants the rest of the
 * dialog suite uses: the whole subject is the window while a request is
 * running, so the fakes below carry real pending state driven by a
 * promise the test decides when to settle.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { act, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import EventDialog, { type EventDialogMode } from '../event-dialog';
import { SLOW_PENDING_MS } from '../lib/use-slow-pending';

type CalEventLike = components['schemas']['MyCalendarEventResponse'];
type EventDetail = components['schemas']['EventResponse'];

/* ── a promise the test settles ───────────────────────────────── */

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/* ── mocks ────────────────────────────────────────────────────── */

const mocks = vi.hoisted(() => ({
  createEvent: vi.fn(),
  updateEvent: vi.fn(),
  deleteEvent: vi.fn(),
  createTask: vi.fn(),
  rememberCalendarChoice: vi.fn(),
  refreshCalendar: vi.fn(),
  toastShow: vi.fn(),
  eventDetail: vi.fn(),
  confirmAction: vi.fn(),
}));

vi.mock('../api', async () => {
  const { useState: useReactState } = await import('react');

  /**
   * A mutation that is actually pending while its promise is unsettled.
   *
   * `isPending` is React state rather than a flag the test flips, so the
   * dialog re-renders on the same transition the real hook would give it
   * and the buttons under test are the ones a user would be looking at.
   */
  function useFakeMutation(send: (args: never) => Promise<unknown>): {
    isPending: boolean;
    mutateAsync: (args: never) => Promise<unknown>;
  } {
    const [pending, setPending] = useReactState(false);
    return {
      isPending: pending,
      mutateAsync: async (args: never) => {
        setPending(true);
        try {
          return await send(args);
        } finally {
          setPending(false);
        }
      },
    };
  }

  return {
    useCalendarsQuery: () => ({
      data: [{ id: 'cal-1', name: 'Primary', role: 'owner' }],
      isLoading: false,
    }),
    useDefaultCalendarId: () => 'cal-1',
    rememberCalendarChoice: mocks.rememberCalendarChoice,
    useRefreshCalendar: () => mocks.refreshCalendar,
    useCreateEvent: () => useFakeMutation(mocks.createEvent),
    useUpdateEvent: () => useFakeMutation(mocks.updateEvent),
    useDeleteEvent: () => useFakeMutation(mocks.deleteEvent),
    useCreateCalendarTask: () => useFakeMutation(mocks.createTask),
    useEventDetailQuery: () => mocks.eventDetail(),
  };
});

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('../../../lib/confirm-action', () => ({
  confirmAction: mocks.confirmAction,
}));

vi.mock('../attendees-api', () => ({
  useAttendeesQuery: () => ({ data: [], isLoading: false }),
  useAddAttendeesMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useRemoveAttendeeMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateOwnRsvpMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useToggleCanEditMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateAttendeeInviteMutation: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

vi.mock('../../workspaces/api', () => ({
  useWorkspaceMembersQuery: () => ({ data: [], isLoading: false }),
}));

/* ── fixtures ─────────────────────────────────────────────────── */

/** The start the series rule gave the occurrence the grid was clicked on. */
const OCCURRENCE_START = Date.UTC(2030, 6, 6, 9) / 1000;

function editMode(): EventDialogMode {
  const base: CalEventLike = {
    id: 'evt-1',
    title: 'Existing event',
    kind: 'event',
    calendarId: 'cal-1',
    workspaceId: 'ws-1',
    workspaceName: 'Workspace',
    timezone: 'UTC',
    allDay: false,
    showAs: 'busy',
    visibility: 'default',
    createdAt: 1_700_000_000,
    startAt: Date.UTC(2030, 5, 15, 9) / 1000,
    endAt: Date.UTC(2030, 5, 15, 10) / 1000,
  } as CalEventLike;
  return {
    kind: 'edit',
    eventId: base.id,
    calendarId: base.calendarId,
    initialKind: 'event',
    event: base,
  };
}

/** Edit mode as the grid opens it for one occurrence of a repeating row. */
function recurringEditMode(): EventDialogMode {
  const base = editMode();
  if (base.kind !== 'edit') throw new Error('editMode must produce an edit-mode dialog');
  return { ...base, occurrence: { originalStartAt: OCCURRENCE_START } };
}

function withDetail(overrides: Partial<EventDetail> = {}): void {
  mocks.eventDetail.mockReturnValue({
    data: {
      id: 'evt-1',
      title: 'Existing event',
      kind: 'event',
      timezone: 'UTC',
      allDay: false,
      showAs: 'busy',
      flexibility: 'fixed',
      visibility: 'default',
      createdAt: 1_700_000_000,
      startAt: Date.UTC(2030, 5, 15, 9) / 1000,
      endAt: Date.UTC(2030, 5, 15, 10) / 1000,
      ...overrides,
    } as EventDetail,
    isLoading: false,
  });
}

function renderDialog(overrides: Partial<Parameters<typeof EventDialog>[0]> = {}): ReactElement {
  return (
    <EventDialog
      open
      zone={Zone.utc()}
      workspaceId="ws-1"
      projects={[{ id: 'proj-1', name: 'Alpha' }] as Parameters<typeof EventDialog>[0]['projects']}
      mode={overrides.mode ?? editMode()}
      onClose={overrides.onClose ?? vi.fn()}
      onSaved={overrides.onSaved ?? vi.fn()}
      {...overrides}
    />
  );
}

function button(name: string): HTMLButtonElement {
  return screen.getByRole('button', { name }) as HTMLButtonElement;
}

beforeEach(() => {
  mocks.createEvent.mockReset().mockResolvedValue({ id: 'evt-new' });
  mocks.updateEvent.mockReset().mockResolvedValue({ id: 'evt-1' });
  mocks.deleteEvent.mockReset().mockResolvedValue(undefined);
  mocks.createTask.mockReset().mockResolvedValue({ id: 'task-new' });
  mocks.rememberCalendarChoice.mockReset();
  mocks.refreshCalendar.mockReset();
  mocks.toastShow.mockReset();
  mocks.confirmAction.mockReset().mockResolvedValue(true);
  mocks.eventDetail.mockReset().mockReturnValue({ data: undefined, isLoading: false });
});

afterEach(() => {
  vi.useRealTimers();
});

/* ── the scope step ───────────────────────────────────────────── */

describe('the scope choice while its write is in flight', () => {
  /**
   * Drives a repeating event as far as a scoped save that has left but
   * not come back, and hands the test the promise that ends it.
   */
  async function startScopedSave(
    onClose: () => void = vi.fn(),
  ): Promise<{ user: ReturnType<typeof userEvent.setup>; pending: Deferred<unknown> }> {
    const pending = deferred<unknown>();
    mocks.updateEvent.mockReturnValue(pending.promise);
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);

    const user = userEvent.setup();
    renderWithProviders(renderDialog({ mode: recurringEditMode(), onClose }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(button('action.submit.edit'));
    await user.click(button('recurrence.scope.save.confirm'));

    return { user, pending };
  }

  it('leaves cancel reachable while the confirm is locked', async () => {
    await startScopedSave();

    // The confirm is locked on purpose: the question has been answered,
    // and answering it twice writes twice.
    expect(button('recurrence.scope.save.pending').hasAttribute('disabled')).toBe(true);
    // Cancel is the exit. Taking it away exactly when the server stops
    // answering is what makes the dialog a trap.
    expect(button('recurrence.scope.cancel').hasAttribute('disabled')).toBe(false);
  });

  it('reads as busy rather than merely greyed out', async () => {
    await startScopedSave();

    const confirm = button('recurrence.scope.save.pending');
    expect(confirm.getAttribute('aria-busy')).toBe('true');
    // A disabled button with unchanged copy says the same thing at 200ms
    // and at 20 seconds.
    expect(screen.queryByRole('button', { name: 'recurrence.scope.save.confirm' })).toBeNull();
  });

  it('dismisses the whole stack and re-reads the grid, without waiting for the server', async () => {
    const onClose = vi.fn();
    const { user } = await startScopedSave(onClose);

    await user.click(button('recurrence.scope.cancel'));

    // Out of both dialogs, with no answer from the server.
    expect(
      screen.queryByRole('radio', { name: 'recurrence.scope.option.series.label' }),
    ).toBeNull();
    expect(onClose).toHaveBeenCalledTimes(1);
    // Nothing recalls a request that has left, so the honest recovery is
    // to re-read rather than to assume either outcome.
    expect(mocks.refreshCalendar).toHaveBeenCalledTimes(1);
  });

  it('lets the abandoned write finish and still reports what it did', async () => {
    const onClose = vi.fn();
    const { user, pending } = await startScopedSave(onClose);

    await user.click(button('recurrence.scope.cancel'));
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve({ id: 'evt-1' });
      await pending.promise;
    });

    // Aborting would not un-send the request — a scoped write against a
    // series may well have committed — so the request is left to finish
    // and its outcome is reported when it arrives.
    expect(mocks.toastShow).toHaveBeenCalledWith({
      tone: 'success',
      message: 'toast.updated.event',
    });
  });

  it('says something once the wait is long enough to look like a freeze', async () => {
    // `shouldAdvanceTime` keeps the runner's own timers moving; without
    // it the frozen clock takes the test timeout down with it.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const pendingSave = deferred<unknown>();
    mocks.updateEvent.mockReturnValue(pendingSave.promise);
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);

    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(button('action.submit.edit'));
    await user.click(button('recurrence.scope.save.confirm'));

    // Silence while the wait is still ordinary.
    expect(screen.queryByRole('status')).toBeNull();

    await act(async () => {
      vi.advanceTimersByTime(SLOW_PENDING_MS);
    });

    // One region, not two. Both dialogs portal into the same host, and
    // the overlay lock leaves that host reachable on purpose, so a
    // notice on the form underneath would be read out alongside this one
    // while being invisible.
    const notices = screen.getAllByRole('status');
    expect(notices).toHaveLength(1);
    const notice = notices[0] as HTMLElement;
    expect(notice.textContent).toBe('recurrence.scope.save.slow');
    expect(notice.getAttribute('aria-live')).toBe('polite');
  });
});

/* ── the edit form underneath ─────────────────────────────────── */

describe('the edit dialog while its own write is in flight', () => {
  it('stays escapable, and does not offer to discard edits that are already on the wire', async () => {
    const onClose = vi.fn();
    const pending = deferred<unknown>();
    mocks.updateEvent.mockReturnValue(pending.promise);
    withDetail();

    const user = userEvent.setup();
    renderWithProviders(renderDialog({ mode: editMode(), onClose }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(button('action.submit.edit'));

    // The commit is locked and says so; the way out is not.
    expect(button('action.submit.saving').hasAttribute('disabled')).toBe(true);
    const cancel = button('action.cancel');
    expect(cancel.hasAttribute('disabled')).toBe(false);

    await user.click(cancel);

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mocks.refreshCalendar).toHaveBeenCalledTimes(1);
    // The edits are not being thrown away — they are on their way to the
    // server — so the discard prompt would be asking the wrong question.
    expect(mocks.confirmAction).not.toHaveBeenCalled();
  });
});
