/**
 * Tests for the unified calendar EventDialog.
 *
 * The dialog's kind-switching state machine, validation, and submit
 * payload shape are exercised here. We mock the `./api` module (the
 * three mutation hooks + the calendar list hook) so the tests stay
 * independent of the generated SDKs and from a running backend.
 */

import type { components } from '@nodate-flow/sdk';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import EventDialog, { type EventDialogMode, presetToRRule } from '../event-dialog';

type CreateEventInput = components['schemas']['CreateEventInputBody'];
type PatchEventInput = components['schemas']['PatchEventInputBody'];
type CalEventLike = components['schemas']['MyCalendarEventResponse'];
type EventDetail = components['schemas']['EventResponse'];

/* ── mocks ────────────────────────────────────────────────────── */

// `vi.mock` factories are hoisted; bind shared handles via `vi.hoisted`
// so they exist when the factory runs.
const mocks = vi.hoisted(() => ({
  createEvent: vi.fn(),
  updateEvent: vi.fn(),
  deleteEvent: vi.fn(),
  createTask: vi.fn(),
  rememberCalendarChoice: vi.fn(),
  toastShow: vi.fn(),
  eventDetail: vi.fn(),
}));

vi.mock('../api', () => ({
  useCalendarsQuery: () => ({
    data: [
      { id: 'cal-1', name: 'Primary', role: 'owner' },
      { id: 'cal-2', name: 'Team', role: 'editor' },
    ],
    isLoading: false,
  }),
  useDefaultCalendarId: () => 'cal-1',
  rememberCalendarChoice: mocks.rememberCalendarChoice,
  useCreateEvent: () => ({ mutateAsync: mocks.createEvent, isPending: false }),
  useUpdateEvent: () => ({ mutateAsync: mocks.updateEvent, isPending: false }),
  useDeleteEvent: () => ({ mutateAsync: mocks.deleteEvent, isPending: false }),
  useCreateCalendarTask: () => ({ mutateAsync: mocks.createTask, isPending: false }),
  useEventDetailQuery: () => mocks.eventDetail(),
}));

// The toast implementation isn't under test — silence it.
vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

// AttendeesSection pulls workspace members + per-event attendees via
// react-query. Neither is under test for the dialog state machine, so
// stub them out with empty resolved data to keep the edit-mode dialog
// renderable without a backend.
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

/* ── helpers ──────────────────────────────────────────────────── */

function createMode(): EventDialogMode {
  return { kind: 'create', date: '2030-06-15', initialItemKind: 'event' };
}

function editMode(event: Partial<CalEventLike> = {}): EventDialogMode {
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
    startAt: Math.floor(new Date('2030-06-15T09:00:00').getTime() / 1000),
    endAt: Math.floor(new Date('2030-06-15T10:00:00').getTime() / 1000),
    ...event,
  } as CalEventLike;
  return {
    kind: 'edit',
    eventId: base.id,
    calendarId: base.calendarId,
    initialKind: 'event',
    event: base,
  };
}

/**
 * The full event row the dialog hydrates from. Distinct from the grid
 * aggregate {@link CalEventLike} in that it carries `memo`, which is the
 * whole reason the dialog re-reads the event instead of trusting the
 * aggregate it was opened with.
 */
function eventDetail(overrides: Partial<EventDetail> = {}): EventDetail {
  return {
    id: 'evt-1',
    title: 'Existing event',
    kind: 'event',
    timezone: 'UTC',
    allDay: false,
    showAs: 'busy',
    flexibility: 'fixed',
    visibility: 'default',
    createdAt: 1_700_000_000,
    startAt: Math.floor(new Date('2030-06-15T09:00:00').getTime() / 1000),
    endAt: Math.floor(new Date('2030-06-15T10:00:00').getTime() / 1000),
    ...overrides,
  } as EventDetail;
}

/** Resolve the detail query with the given row on the next render. */
function withDetail(overrides: Partial<EventDetail> = {}): void {
  mocks.eventDetail.mockReturnValue({ data: eventDetail(overrides), isLoading: false });
}

function renderDialog(overrides: Partial<Parameters<typeof EventDialog>[0]> = {}): ReactElement {
  return (
    <EventDialog
      open
      workspaceId="ws-1"
      projects={[{ id: 'proj-1', name: 'Alpha' }] as Parameters<typeof EventDialog>[0]['projects']}
      mode={overrides.mode ?? createMode()}
      onClose={overrides.onClose ?? vi.fn()}
      onSaved={overrides.onSaved ?? vi.fn()}
      {...overrides}
    />
  );
}

beforeEach(() => {
  mocks.createEvent.mockReset().mockResolvedValue({ id: 'evt-new' });
  mocks.updateEvent.mockReset().mockResolvedValue({ id: 'evt-1' });
  mocks.deleteEvent.mockReset().mockResolvedValue(undefined);
  mocks.createTask.mockReset().mockResolvedValue({ id: 'task-new' });
  mocks.rememberCalendarChoice.mockReset();
  mocks.toastShow.mockReset();
  // Create mode never fetches; edit-mode tests opt in via `withDetail`.
  mocks.eventDetail.mockReset().mockReturnValue({ data: undefined, isLoading: false });
});

/* ── tests ────────────────────────────────────────────────────── */

describe('<EventDialog>', () => {
  it('switching kind preserves the title and drops block-only fields', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog({ mode: { kind: 'create', date: '2030-06-15' } }));

    // Type a title in the common field.
    const titleInput = screen.getByRole('textbox', { name: 'field.title' });
    await user.type(titleInput, 'Cross-kind title');

    // Switch to Block kind — block label chips appear.
    await user.click(screen.getByRole('radio', { name: 'kind.block' }));
    expect(screen.getByText('blockLabel.preset.working')).toBeDefined();

    // Pick the Custom preset and type a label, then switch to Event.
    await user.click(screen.getByText('blockLabel.preset.custom'));
    await user.click(screen.getByRole('radio', { name: 'kind.event' }));

    // Event kind has no block label UI.
    expect(screen.queryByText('blockLabel.preset.working')).toBeNull();
    // Title is preserved across the switch.
    expect(screen.getByDisplayValue('Cross-kind title')).toBeDefined();

    // Going back to Block should reset the preset to 'working' (block-only
    // fields dropped on exit).
    await user.click(screen.getByRole('radio', { name: 'kind.block' }));
    const workingChip = screen.getByText('blockLabel.preset.working').closest('button');
    expect(workingChip?.getAttribute('aria-pressed')).toBe('true');
  });

  it('validates empty title on submit', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    renderWithProviders(renderDialog({ onSaved }));

    // Hit submit without filling title.
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    // Error copy is surfaced and no POST fires.
    expect(screen.getByText('validation.titleRequired')).toBeDefined();
    expect(mocks.createEvent).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
  });

  it('create-mode submit builds an event payload and remembers calendar', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    renderWithProviders(renderDialog({ onSaved }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Planning call');
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    expect(mocks.createEvent).toHaveBeenCalledTimes(1);
    const args = mocks.createEvent.mock.calls[0]?.[0] as {
      workspaceId: string;
      calendarId: string;
      body: CreateEventInput;
    };
    expect(args.workspaceId).toBe('ws-1');
    expect(args.calendarId).toBe('cal-1');
    expect(args.body.title).toBe('Planning call');
    expect(args.body.kind).toBe('event');
    expect(args.body.showAs).toBe('busy');
    expect(args.body.allDay).toBe(false);
    expect(typeof args.body.startAt).toBe('number');
    expect(typeof args.body.endAt).toBe('number');
    const startAt = args.body.startAt ?? 0;
    const endAt = args.body.endAt ?? 0;
    expect(endAt > startAt).toBe(true);

    expect(mocks.rememberCalendarChoice).toHaveBeenCalledWith('ws-1', 'cal-1');
    expect(onSaved).toHaveBeenCalled();
  });

  it('defaults flexibility to fixed so an unanswered event is not offered up', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog());

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Planning call');
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    const args = mocks.createEvent.mock.calls[0]?.[0] as { body: CreateEventInput };
    expect(args.body.flexibility).toBe('fixed');
  });

  it('sends flexibility without disturbing showAs, which free/busy consumers read', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog());

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Planning call');
    await user.click(screen.getByRole('radio', { name: 'flexibility.negotiable' }));
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    const args = mocks.createEvent.mock.calls[0]?.[0] as { body: CreateEventInput };
    expect(args.body.flexibility).toBe('negotiable');
    // The whole point of a second column: marking an event movable must
    // not quietly downgrade it to 'tentative' for anyone reading the
    // calendar from outside.
    expect(args.body.showAs).toBe('busy');
  });

  it('edit-mode seeds the control from the stored value', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      renderDialog({ mode: editMode({ flexibility: 'conditional' } as Partial<CalEventLike>) }),
    );

    expect(
      screen.getByRole('radio', { name: 'flexibility.conditional' }).getAttribute('aria-checked'),
    ).toBe('true');

    // Only a control the user moved reaches the payload, so drive the
    // one under test before submitting.
    await user.click(screen.getByRole('radio', { name: 'flexibility.negotiable' }));
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));
    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: { flexibility?: string } };
    expect(args.body.flexibility).toBe('negotiable');
  });

  it('create-mode submits a task payload when kind is Task', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog());

    await user.click(screen.getByRole('radio', { name: 'kind.task' }));
    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Ship docs');
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    expect(mocks.createTask).toHaveBeenCalledTimes(1);
    const body = mocks.createTask.mock.calls[0]?.[0] as {
      projectId: string;
      title: string;
      dueOn: string;
    };
    expect(body.projectId).toBe('proj-1');
    expect(body.title).toBe('Ship docs');
    expect(body.dueOn).toBe('2030-06-15');
    expect(mocks.createEvent).not.toHaveBeenCalled();
  });

  it('all-day toggle hides time pickers and restores last time on revert', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog());

    // Open — default event kind renders TimePicker triggers (two of them).
    const initialTimeTriggers = screen.getAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    expect(initialTimeTriggers.length).toBe(2);

    // Flip all-day on — time triggers vanish.
    const switchEl = screen.getByRole('switch');
    await user.click(switchEl);
    expect(screen.queryAllByRole('button', { name: /^\d{2}:\d{2}$/ })).toHaveLength(0);

    // Flip back off — the previous time values come back (09:00, 10:00).
    await user.click(switchEl);
    const restored = screen.getAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    expect(restored.length).toBe(2);
  });

  it('edit-mode prefills fields and shows Delete button', () => {
    renderWithProviders(renderDialog({ mode: editMode() }));

    expect(screen.getByDisplayValue('Existing event')).toBeDefined();
    expect(screen.getByRole('button', { name: 'a11y.delete_button' })).toBeDefined();
    // Submit button text switches to the edit copy.
    expect(screen.getByRole('button', { name: 'action.submit.edit' })).toBeDefined();
  });

  it('edit-mode disables the Task kind in the picker', () => {
    renderWithProviders(renderDialog({ mode: editMode() }));
    const taskRadio = screen.getByRole('radio', { name: 'kind.task' });
    expect(taskRadio.hasAttribute('disabled')).toBe(true);
  });

  it('edit-mode shows the stored memo, which the grid aggregate never carries', async () => {
    withDetail({ memo: 'Vendor quote: 3 weeks lead time, contact Rin' });
    renderWithProviders(renderDialog({ mode: editMode() }));

    const memoBox = await screen.findByRole('textbox', { name: 'field.memo' });
    expect((memoBox as HTMLTextAreaElement).value).toBe(
      'Vendor quote: 3 weeks lead time, contact Rin',
    );
  });

  it('leaves the memo out of the patch body when only the title changed', async () => {
    const user = userEvent.setup();
    withDetail({ memo: 'Vendor quote: 3 weeks lead time, contact Rin' });
    renderWithProviders(renderDialog({ mode: editMode() }));

    const titleInput = screen.getByRole('textbox', { name: 'field.title' });
    await user.clear(titleInput);
    await user.type(titleInput, 'Renamed event');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    expect(mocks.updateEvent).toHaveBeenCalledTimes(1);
    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.title).toBe('Renamed event');
    // An absent field means "leave it alone"; sending the memo here is
    // what used to overwrite a long note with whatever the dialog held.
    expect('memo' in args.body).toBe(false);
  });

  it('sends the memo once the user actually edits it', async () => {
    const user = userEvent.setup();
    withDetail({ memo: 'Vendor quote' });
    renderWithProviders(renderDialog({ mode: editMode() }));

    const memoBox = await screen.findByRole('textbox', { name: 'field.memo' });
    await user.type(memoBox, ' — revised');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.memo).toBe('Vendor quote — revised');
    // Untouched controls stay out of the payload.
    expect('title' in args.body).toBe(false);
    expect('startAt' in args.body).toBe(false);
  });

  it('skips the request entirely when nothing was edited', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    withDetail({ memo: 'Vendor quote' });
    renderWithProviders(renderDialog({ mode: editMode(), onSaved }));

    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    expect(mocks.updateEvent).not.toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalled();
  });

  it('keeps the memo box inert until the stored value arrives', async () => {
    const user = userEvent.setup();
    mocks.eventDetail.mockReturnValue({ data: undefined, isLoading: true });
    renderWithProviders(renderDialog({ mode: editMode() }));

    await user.click(screen.getByRole('button', { name: 'action.moreOptions' }));
    const memoBox = screen.getByRole('textbox', { name: 'field.memo' });
    expect((memoBox as HTMLTextAreaElement).disabled).toBe(true);
    expect(memoBox.getAttribute('aria-busy')).toBe('true');
  });

  it('preserves backend error detail in mutation failure toasts', async () => {
    const user = userEvent.setup();
    mocks.createEvent.mockRejectedValueOnce(new Error('Calendar slug already exists'));
    renderWithProviders(renderDialog());

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Conflicting event');
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    expect(mocks.toastShow).toHaveBeenCalledWith({
      tone: 'danger',
      message: 'Calendar slug already exists',
    });
  });
});

describe('presetToRRule', () => {
  it('returns null for the none preset', () => {
    expect(presetToRRule('none', '2030-06-15')).toBeNull();
  });

  it('emits canonical lowercase freq tokens matching the expander contract', () => {
    expect(presetToRRule('daily', '2030-06-15')).toEqual({ freq: 'daily' });
    expect(presetToRRule('weekly', '2030-06-15')).toEqual({ freq: 'weekly' });
    expect(presetToRRule('monthly', '2030-06-15')).toEqual({ freq: 'monthly' });
    expect(presetToRRule('yearly', '2030-06-15')).toEqual({ freq: 'yearly' });
  });

  it('emits lowercase weekday byDay tokens for the weekdays preset', () => {
    expect(presetToRRule('weekdays', '2030-06-15')).toEqual({
      freq: 'weekly',
      byDay: ['mo', 'tu', 'we', 'th', 'fr'],
    });
  });

  it('never emits an uppercase freq token', () => {
    for (const preset of ['daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const) {
      const rule = presetToRRule(preset, '2030-06-15');
      expect(rule).not.toBeNull();
      const freq = (rule as { freq: string }).freq;
      expect(freq).toBe(freq.toLowerCase());
    }
  });
});
