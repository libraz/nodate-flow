/**
 * Tests for the unified calendar EventDialog.
 *
 * The dialog's kind-switching state machine, validation, and submit
 * payload shape are exercised here. We mock the `./api` module (the
 * three mutation hooks + the calendar list hook) so the tests stay
 * independent of the generated SDKs and from a running backend.
 */

import type { components } from '@nodate-flow/sdk';
import { Zone } from '@nodate-flow/ui/time';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import type { i18n as I18nInstance } from 'i18next';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { wallClockToUnix } from '../../../lib/date-utils';
import { formatDate } from '../../../lib/format';
import EventDialog, {
  type EventDialogMode,
  occurrenceRange,
  presetToRRule,
  rruleToPreset,
  weekdayName,
} from '../event-dialog';

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
  refreshCalendar: vi.fn(),
  toastShow: vi.fn(),
  eventDetail: vi.fn(),
  confirmAction: vi.fn(),
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
  useRefreshCalendar: () => mocks.refreshCalendar,
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

// `confirmAction` resolves through a `ConfirmProvider` the test provider
// tree does not mount, so the real one would hang forever. Stubbing it
// also lets a test assert that a repeating delete never reaches it.
vi.mock('../../../lib/confirm-action', () => ({
  confirmAction: mocks.confirmAction,
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
    startAt: Date.UTC(2030, 5, 15, 9) / 1000,
    endAt: Date.UTC(2030, 5, 15, 10) / 1000,
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
 * The start the series rule gave the occurrence the grid was clicked on.
 * Deliberately not the first occurrence and not the value any control in
 * the form holds, so a payload that picked up the form's start instead
 * fails rather than coincidentally matching.
 */
const OCCURRENCE_START = Date.UTC(2030, 6, 6, 9) / 1000;

/**
 * Edit mode as the month grid opens it for a repeating row: the event
 * plus the occurrence that was drawn.
 */
function recurringEditMode(
  occurrenceStart: number = OCCURRENCE_START,
  event: Partial<CalEventLike> = {},
): EventDialogMode {
  const base = editMode(event);
  if (base.kind !== 'edit') throw new Error('editMode must produce an edit-mode dialog');
  return { ...base, occurrence: { originalStartAt: occurrenceStart } };
}

/** The text a date control shows for a `YYYY-MM-DD` key. */
function dayTrigger(dayKey: string): string {
  return formatDate(dayKey, 'en');
}

/**
 * Load real copy for a namespace onto the shared test i18n instance,
 * returning the undo.
 *
 * Every other assertion here reads the key back, which cannot tell a
 * filled placeholder from an unfilled one — both render as
 * `recurrence.preset.weekly`. With the actual string loaded, a value the
 * render site never supplies shows up as the literal braces a user would
 * see. The caller must run the undo so later tests keep reading keys.
 */
function loadCopy(bundle: Record<string, unknown>): () => void {
  // The render helper keeps its instance module-private, so the only way
  // in is a component that asks the provider for it.
  const holder: { instance: I18nInstance | null } = { instance: null };
  function Probe(): null {
    holder.instance = useTranslation().i18n;
    return null;
  }
  renderWithProviders(<Probe />).unmount();
  const instance = holder.instance;
  if (instance === null) throw new Error('the test i18n instance was not reachable');
  instance.addResourceBundle('en', 'calendar-events', bundle, true, true);
  return () => {
    instance.removeResourceBundle('en', 'calendar-events');
  };
}

/** The three-way choice, by the option the caller wants to pick. */
function scopeRadio(scope: 'occurrence' | 'thisAndFollowing' | 'series'): HTMLElement {
  return screen.getByRole('radio', { name: `recurrence.scope.option.${scope}.label` });
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
    startAt: Date.UTC(2030, 5, 15, 9) / 1000,
    endAt: Date.UTC(2030, 5, 15, 10) / 1000,
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
      zone={Zone.utc()}
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
  mocks.refreshCalendar.mockReset();
  mocks.toastShow.mockReset();
  mocks.confirmAction.mockReset().mockResolvedValue(true);
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

  it('shows the stored repeat rule instead of claiming the event never repeats', async () => {
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: editMode() }));

    // The rule lives behind the disclosure, which opens itself when there
    // is one — a repeat the user cannot see is a repeat they cannot stop.
    const select = (await screen.findByRole('combobox', {
      name: 'field.recurrence',
    })) as HTMLSelectElement;
    expect(select.value).toBe('weekly');
  });

  it('labels a rule no preset reproduces as custom rather than as no repeat', async () => {
    withDetail({ recurrenceRule: { freq: 'weekly', interval: 2 } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: editMode() }));

    const select = (await screen.findByRole('combobox', {
      name: 'field.recurrence',
    })) as HTMLSelectElement;
    expect(select.value).toBe('custom');
  });

  it('clears the stored rule when the user picks "does not repeat"', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'daily' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: editMode() }));

    const select = await screen.findByRole('combobox', { name: 'field.recurrence' });
    await user.selectOptions(select, 'none');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    // Omitting the field means "leave it alone" in a PATCH, so stopping a
    // daily standup needs the explicit clear — without it the only way out
    // was deleting the series.
    expect(args.body.clear).toContain('recurrenceRule');
    expect('recurrenceRule' in args.body).toBe(false);
  });

  it('leaves an untouched rule out of the patch body now that it is seeded', async () => {
    const user = userEvent.setup();
    withDetail({
      recurrenceRule: { freq: 'weekly', interval: 2 },
      memo: 'Vendor quote',
    } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: editMode() }));

    const memoBox = await screen.findByRole('textbox', { name: 'field.memo' });
    await user.type(memoBox, ' — revised');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.memo).toBe('Vendor quote — revised');
    // Seeding the control must not turn every save into a rewrite of the
    // rule — and the every-other-week rule the presets cannot express must
    // survive a save that had nothing to do with it.
    expect('recurrenceRule' in args.body).toBe(false);
    expect(args.body.clear ?? []).not.toContain('recurrenceRule');
  });

  /* ── zone boundaries ────────────────────────────────────────── */

  // The dialog is handed a zone that is deliberately not the runner's,
  // so an implementation that resolved wall clocks in the browser's zone
  // fails these on every machine rather than only on some.
  const eventZone = Zone.resolve('America/New_York');

  it('resolves a submitted wall clock in the zone it stamps on the event', () => {
    // 09:00-10:00 on 2030-06-15 in New York is 13:00-14:00Z. Sending an
    // instant resolved in the browser's zone alongside a `timezone` of
    // America/New_York stores an event that contradicts its own label.
    expect(wallClockToUnix('2030-06-15', '09:00', eventZone)).toBe(
      Date.UTC(2030, 5, 15, 13, 0, 0, 0) / 1000,
    );
  });

  it('stamps the zone it resolved the instants in', async () => {
    const user = userEvent.setup();
    renderWithProviders(renderDialog({ zone: eventZone }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), 'Planning call');
    await user.click(screen.getByRole('button', { name: 'action.submit.create' }));

    const args = mocks.createEvent.mock.calls[0]?.[0] as { body: CreateEventInput };
    expect(args.body.timezone).toBe('America/New_York');
    // The default create times are 09:00-10:00 on the clicked date, and
    // they have to be resolved in the zone the row declares.
    expect(args.body.startAt).toBe(wallClockToUnix('2030-06-15', '09:00', eventZone));
    expect(args.body.endAt).toBe(wallClockToUnix('2030-06-15', '10:00', eventZone));
  });

  it('opens an event on the wall clock its zone gives it', async () => {
    // 2030-06-15T13:00Z is 09:00 in New York. Seeding the form from the
    // browser's zone shows a time the event does not have.
    withDetail({
      startAt: Date.UTC(2030, 5, 15, 13) / 1000,
      endAt: Date.UTC(2030, 5, 15, 14) / 1000,
    });
    renderWithProviders(renderDialog({ mode: editMode(), zone: eventZone }));

    const times = await screen.findAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    expect(times.map((el) => el.textContent)).toEqual(['09:00', '10:00']);
  });

  it('does not move an event that was opened and saved without touching the time', async () => {
    // The seed and the submit are one round trip. When only one of them
    // was corrected, every edit shifted the event by the offset between
    // the two zones — a silent rewrite of data nobody touched.
    const user = userEvent.setup();
    const startAt = Date.UTC(2030, 5, 15, 13) / 1000;
    const endAt = Date.UTC(2030, 5, 15, 14) / 1000;
    withDetail({ startAt, endAt });
    renderWithProviders(renderDialog({ mode: editMode(), zone: eventZone }));

    // Move a time control so the range reaches the payload at all, then
    // put it back where the stored event had it.
    const [startTrigger] = await screen.findAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    await user.click(startTrigger as HTMLElement);
    await user.click(screen.getByRole('option', { name: '08:00' }));
    await user.click(startTrigger as HTMLElement);
    await user.click(screen.getByRole('option', { name: '09:00' }));
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.startAt).toBe(startAt);
    expect(args.body.endAt).toBe(endAt);
  });

  /* ── occurrence seeding ─────────────────────────────────────── */

  // A series is stored once, as its first occurrence on 15 June, while
  // the grid draws a pill per instance the rule produces. The dialog is
  // opened here from the instance the rule gave 6 July.

  it('opens an occurrence of a series on that occurrence, not on the stored series start', async () => {
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    // The rule is behind the disclosure, which opens itself once the
    // authoritative row has landed — so awaiting it awaits the seed.
    await screen.findByRole('combobox', { name: 'field.recurrence' });

    // Both date controls sit on the day the user clicked…
    expect(screen.getAllByRole('button', { name: dayTrigger('2030-07-06') })).toHaveLength(2);
    // …and neither still shows the series' own start.
    expect(screen.queryAllByRole('button', { name: dayTrigger('2030-06-15') })).toHaveLength(0);
    // The duration is the one part of the range an occurrence does not
    // state, so it comes from the stored row: 09:00-10:00.
    const times = screen.getAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    expect(times.map((el) => el.textContent)).toEqual(['09:00', '10:00']);
  });

  it('opens an all-day occurrence on its own days, keeping the span', async () => {
    const startAt = Date.UTC(2030, 5, 15) / 1000;
    const endAt = Date.UTC(2030, 5, 16) / 1000;
    withDetail({
      allDay: true,
      startAt,
      endAt,
      recurrenceRule: { freq: 'weekly' },
    } as Partial<EventDetail>);
    renderWithProviders(
      renderDialog({
        mode: recurringEditMode(Date.UTC(2030, 6, 6) / 1000, { allDay: true, startAt, endAt }),
      }),
    );

    await screen.findByRole('combobox', { name: 'field.recurrence' });

    // A two-day span moved whole: 6-7 July rather than 15-16 June.
    expect(screen.getByRole('button', { name: dayTrigger('2030-07-06') })).toBeDefined();
    expect(screen.getByRole('button', { name: dayTrigger('2030-07-07') })).toBeDefined();
    expect(screen.queryByRole('button', { name: dayTrigger('2030-06-15') })).toBeNull();
    // An all-day row has no time controls to seed.
    expect(screen.queryAllByRole('button', { name: /^\d{2}:\d{2}$/ })).toHaveLength(0);
  });

  it('seeds an occurrence without marking the date controls edited', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    const titleInput = screen.getByRole('textbox', { name: 'field.title' });
    await user.clear(titleInput);
    await user.type(titleInput, 'Renamed occurrence');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.save.confirm' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.title).toBe('Renamed occurrence');
    // A present field is an instruction to set it, and one moved time
    // control re-sends the whole range. Seeding from the occurrence must
    // not turn every scoped save into an override that pins dates the
    // user never touched.
    expect('startAt' in args.body).toBe(false);
    expect('endAt' in args.body).toBe(false);
    expect('allDay' in args.body).toBe(false);
  });

  it('treats a freshly opened occurrence as unedited when the dialog is dismissed', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode(), onClose }));

    await screen.findByRole('combobox', { name: 'field.recurrence' });
    await user.click(screen.getByRole('button', { name: 'action.cancel' }));

    // Nothing was moved, so there is nothing to offer to discard.
    expect(mocks.confirmAction).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('opens the master occurrence exactly where the stored row sits', async () => {
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode(Date.UTC(2030, 5, 15, 9) / 1000) }));

    await screen.findByRole('combobox', { name: 'field.recurrence' });

    expect(screen.getAllByRole('button', { name: dayTrigger('2030-06-15') })).toHaveLength(2);
    const times = screen.getAllByRole('button', { name: /^\d{2}:\d{2}$/ });
    expect(times.map((el) => el.textContent)).toEqual(['09:00', '10:00']);
  });

  it('opens an event that does not repeat on the dates it stores', async () => {
    withDetail({ memo: 'Vendor quote' });
    renderWithProviders(renderDialog({ mode: editMode() }));

    await screen.findByRole('textbox', { name: 'field.memo' });

    expect(screen.getAllByRole('button', { name: dayTrigger('2030-06-15') })).toHaveLength(2);
  });

  /* ── recurrence copy ────────────────────────────────────────── */

  it('names the weekday a weekly repeat falls on instead of showing a placeholder', async () => {
    const restore = loadCopy({ recurrence: { preset: { weekly: 'Weekly on {day}' } } });
    try {
      // 17 June 2030 is a Monday.
      withDetail({
        startAt: Date.UTC(2030, 5, 17, 9) / 1000,
        endAt: Date.UTC(2030, 5, 17, 10) / 1000,
        recurrenceRule: { freq: 'weekly' },
      } as Partial<EventDetail>);
      renderWithProviders(renderDialog({ mode: editMode() }));

      const select = await screen.findByRole('combobox', { name: 'field.recurrence' });
      const weekly = within(select).getByRole('option', { name: 'Weekly on Monday' });
      // The braces are what the option read before the day was supplied.
      expect(weekly.textContent).not.toContain('{');
    } finally {
      restore();
    }
  });

  /* ── recurring scope choice ─────────────────────────────────── */

  it('saves a one-off event without asking which occurrences to touch', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    withDetail();
    renderWithProviders(renderDialog({ mode: editMode(), onSaved }));

    const titleInput = screen.getByRole('textbox', { name: 'field.title' });
    await user.clear(titleInput);
    await user.type(titleInput, 'Renamed event');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    // No extra step: the patch went out on the first click, carrying no
    // scope at all — the shape every save had before the choice existed.
    expect(
      screen.queryByRole('radio', { name: 'recurrence.scope.option.series.label' }),
    ).toBeNull();
    expect(mocks.updateEvent).toHaveBeenCalledTimes(1);
    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect('scope' in args.body).toBe(false);
    expect('occurrenceStart' in args.body).toBe(false);
    expect(onSaved).toHaveBeenCalled();
  });

  it('deletes a one-off event through the plain confirm, with no scope', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    withDetail();
    renderWithProviders(renderDialog({ mode: editMode(), onSaved }));

    await user.click(screen.getByRole('button', { name: 'a11y.delete_button' }));

    expect(mocks.confirmAction).toHaveBeenCalledTimes(1);
    expect(mocks.deleteEvent).toHaveBeenCalledTimes(1);
    const args = mocks.deleteEvent.mock.calls[0]?.[0] as Record<string, unknown>;
    expect('scope' in args).toBe(false);
    expect('occurrenceStart' in args).toBe(false);
    expect(onSaved).toHaveBeenCalled();
  });

  it('asks a repeating event which occurrences a save reaches, then sends them', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    const titleInput = screen.getByRole('textbox', { name: 'field.title' });
    await user.clear(titleInput);
    await user.type(titleInput, 'Renamed occurrence');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    // Nothing is written until the question is answered.
    expect(mocks.updateEvent).not.toHaveBeenCalled();
    // The least destructive option is the one a mis-click lands on.
    expect((scopeRadio('occurrence') as HTMLInputElement).checked).toBe(true);

    await user.click(scopeRadio('thisAndFollowing'));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.save.confirm' }));

    expect(mocks.updateEvent).toHaveBeenCalledTimes(1);
    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.title).toBe('Renamed occurrence');
    expect(args.body.scope).toBe('thisAndFollowing');
    // The occurrence the grid was clicked on, not the start the form is
    // holding — sending the latter overrides an occurrence nobody opened.
    expect(args.body.occurrenceStart).toBe(OCCURRENCE_START);
  });

  it('sends no occurrence when the save is aimed at the whole series', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));
    await user.click(scopeRadio('series'));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.save.confirm' }));

    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.scope).toBe('series');
    // No single instant identifies "all of them"; sending one would be a
    // claim the request does not mean.
    expect('occurrenceStart' in args.body).toBe(false);
  });

  it('asks a repeating event which occurrences a delete reaches, instead of confirming twice', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode(), onSaved }));

    await user.click(screen.getByRole('button', { name: 'a11y.delete_button' }));

    // The choice replaces the confirm rather than stacking on top of it.
    expect(mocks.confirmAction).not.toHaveBeenCalled();
    expect(mocks.deleteEvent).not.toHaveBeenCalled();

    await user.click(scopeRadio('occurrence'));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.delete.confirm' }));

    expect(mocks.deleteEvent).toHaveBeenCalledTimes(1);
    expect(mocks.deleteEvent.mock.calls[0]?.[0]).toMatchObject({
      workspaceId: 'ws-1',
      calendarId: 'cal-1',
      eventId: 'evt-1',
      scope: 'occurrence',
      occurrenceStart: OCCURRENCE_START,
    });
    expect(onSaved).toHaveBeenCalled();
  });

  it('cancelling the choice writes nothing and leaves the edit dialog up', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode(), onSaved }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.cancel' }));

    expect(mocks.updateEvent).not.toHaveBeenCalled();
    expect(mocks.deleteEvent).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
    // Back on the form with the edit intact, not dropped on the floor.
    expect(
      screen.queryByRole('radio', { name: 'recurrence.scope.option.series.label' }),
    ).toBeNull();
    expect(screen.getByRole('button', { name: 'action.submit.edit' })).toBeDefined();
  });

  it('opens the choice on the first option and lets Escape back out of it', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    await user.click(screen.getByRole('button', { name: 'a11y.delete_button' }));

    // Focus has to land inside the step, or a keyboard user is answering
    // a question they never reached.
    expect(document.activeElement).toBe(scopeRadio('occurrence'));

    await user.keyboard('{Escape}');

    expect(mocks.deleteEvent).not.toHaveBeenCalled();
    expect(
      screen.queryByRole('radio', { name: 'recurrence.scope.option.occurrence.label' }),
    ).toBeNull();
  });

  it('does not ask when the edit is itself a rewrite of the repeat rule', async () => {
    const user = userEvent.setup();
    withDetail({ recurrenceRule: { freq: 'daily' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode() }));

    const select = await screen.findByRole('combobox', { name: 'field.recurrence' });
    await user.selectOptions(select, 'none');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));

    // Stopping the repeat is a statement about the series, so there is
    // nothing to scope — and the API refuses a scoped patch that touches
    // the rule anyway.
    expect(
      screen.queryByRole('radio', { name: 'recurrence.scope.option.series.label' }),
    ).toBeNull();
    expect(mocks.updateEvent).toHaveBeenCalledTimes(1);
    const args = mocks.updateEvent.mock.calls[0]?.[0] as { body: PatchEventInput };
    expect(args.body.clear).toContain('recurrenceRule');
    expect('scope' in args.body).toBe(false);
  });

  it('surfaces a refused scope instead of closing on it', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    mocks.updateEvent.mockRejectedValueOnce(new Error('This event does not repeat'));
    withDetail({ recurrenceRule: { freq: 'weekly' } } as Partial<EventDetail>);
    renderWithProviders(renderDialog({ mode: recurringEditMode(), onSaved }));

    await user.type(screen.getByRole('textbox', { name: 'field.title' }), '!');
    await user.click(screen.getByRole('button', { name: 'action.submit.edit' }));
    await user.click(screen.getByRole('button', { name: 'recurrence.scope.save.confirm' }));

    expect(mocks.toastShow).toHaveBeenCalledWith({
      tone: 'danger',
      message: 'This event does not repeat',
    });
    expect(onSaved).not.toHaveBeenCalled();
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

describe('occurrenceRange', () => {
  it('leaves a row that was not opened from an occurrence alone', () => {
    expect(occurrenceRange({ startAt: 100, endAt: 700 }, null)).toEqual({
      startAt: 100,
      endAt: 700,
    });
  });

  it('moves the stored span onto the occurrence that was opened', () => {
    expect(occurrenceRange({ startAt: 100, endAt: 700 }, 1_000)).toEqual({
      startAt: 1_000,
      endAt: 1_600,
    });
  });

  it('has nothing to place an end against when the row has no start', () => {
    expect(occurrenceRange({ endAt: 700 }, 1_000)).toEqual({ startAt: null, endAt: 700 });
  });

  it('leaves an absent end absent rather than inventing one', () => {
    expect(occurrenceRange({ startAt: 100 }, 1_000)).toEqual({ startAt: 1_000, endAt: null });
  });
});

describe('weekdayName', () => {
  it('names the weekday in the language being read', () => {
    expect(weekdayName('2030-06-17', 'en')).toBe('Monday');
    expect(weekdayName('2030-06-17', 'ja')).toBe('月曜日');
    expect(weekdayName('2030-06-17', 'zh')).toBe('星期一');
  });

  it('tracks the date it is given', () => {
    expect(weekdayName('2030-06-15', 'en')).toBe('Saturday');
    expect(weekdayName('2030-06-16', 'en')).toBe('Sunday');
  });

  it('says nothing rather than guessing when the value is not a date', () => {
    expect(weekdayName('', 'en')).toBe('');
    expect(weekdayName('not-a-date', 'en')).toBe('');
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

describe('rruleToPreset', () => {
  it('reports no repeat only when there is genuinely no rule', () => {
    expect(rruleToPreset(null)).toBe('none');
    expect(rruleToPreset(undefined)).toBe('none');
  });

  it('round-trips every preset the control can author', () => {
    for (const preset of ['daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const) {
      const rule = presetToRRule(preset, '2030-06-15');
      expect(rruleToPreset(rule)).toBe(preset);
    }
  });

  it('accepts uppercase weekday tokens, which RFC 5545 rules carry', () => {
    expect(rruleToPreset({ freq: 'weekly', byDay: ['MO', 'TU', 'WE', 'TH', 'FR'] })).toBe(
      'weekdays',
    );
  });

  it('refuses to flatten a rule a preset would silently truncate', () => {
    // Each of these says something no preset can, so answering with one
    // would drop that part of the rule the moment the user saved.
    expect(rruleToPreset({ freq: 'weekly', interval: 2 })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', count: 10 })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', until: '2030-12-31' })).toBe('custom');
    expect(rruleToPreset({ freq: 'monthly', byMonthDay: [1, 15] })).toBe('custom');
    expect(rruleToPreset({ freq: 'weekly', byDay: ['mo', 'we'] })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', byDay: ['mo'] })).toBe('custom');
  });

  it('never authors a rule for the value it uses to describe one', () => {
    // Selecting 'custom' has to be a no-op: it names a rule this control
    // did not write and cannot rebuild.
    expect(presetToRRule('custom', '2030-06-15')).toBeNull();
  });
});
