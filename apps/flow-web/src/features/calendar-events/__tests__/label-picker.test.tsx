import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { FALLBACK_EVENT_COLOR } from '../../../lib/source-colors';
import LabelPicker, { type LabelPickerProps } from '../label-picker';

const mocks = vi.hoisted(() => ({ labels: [] as { id: string; name: string; color: string }[] }));

vi.mock('../../calendars/labels-api', () => ({
  useCalendarLabelsQuery: () => ({ data: mocks.labels }),
}));

const LABELS = [
  { id: 'l1', name: 'Client', color: '#E73B3B' },
  { id: 'l2', name: 'Internal', color: '#2273C9' },
];

beforeEach(() => {
  mocks.labels = LABELS;
});

/** Holds the selection the way the dialog does. */
function Harness(
  props: Partial<LabelPickerProps> & { onPick?: (id: string) => void },
): ReactElement {
  const [value, setValue] = useState(props.value ?? '');
  const { onPick, ...rest } = props;
  return (
    <LabelPicker
      workspaceId="ws-1"
      calendarId="cal-1"
      noneColor="#26B5B0"
      {...rest}
      value={value}
      onChange={(id) => {
        setValue(id);
        onPick?.(id);
      }}
    />
  );
}

describe('<LabelPicker>', () => {
  it('renders nothing when the calendar has no labels', () => {
    mocks.labels = [];
    renderWithProviders(<Harness />);
    expect(screen.queryByRole('radiogroup')).toBeNull();
  });

  it('renders nothing when the event details are hidden', () => {
    renderWithProviders(<Harness detailsHidden />);
    expect(screen.queryByRole('radiogroup')).toBeNull();
  });

  it('names the group and starts on none', () => {
    renderWithProviders(<Harness />);
    expect(screen.getByRole('radiogroup', { name: 'field.label' })).toBeTruthy();
    const radios = screen.getAllByRole('radio');
    expect(radios.map((r) => r.textContent)).toEqual(['field.label_none', 'Client', 'Internal']);
    expect(
      screen.getByRole('radio', { name: 'field.label_none' }).getAttribute('aria-checked'),
    ).toBe('true');
  });

  it('draws the none dot in the colour it was given, else the fallback', () => {
    const { unmount } = renderWithProviders(<Harness noneColor="#B3261E" />);
    expect(
      screen.getByRole('radio', { name: 'field.label_none' }).style.getPropertyValue('--evt'),
    ).toBe('#B3261E');
    unmount();
    renderWithProviders(<Harness noneColor="not-a-colour" />);
    expect(
      screen.getByRole('radio', { name: 'field.label_none' }).style.getPropertyValue('--evt'),
    ).toBe(FALLBACK_EVENT_COLOR);
  });

  it('selects a label on click and reports it', async () => {
    const onPick = vi.fn();
    renderWithProviders(<Harness onPick={onPick} />);
    await userEvent.setup().click(screen.getByRole('radio', { name: 'Client' }));
    expect(onPick).toHaveBeenCalledWith('l1');
    expect(screen.getByRole('radio', { name: 'Client' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByRole('radio', { name: 'Client' }).style.getPropertyValue('--evt')).toBe(
      '#E73B3B',
    );
  });

  it('moves with the arrow keys on a roving tabindex', async () => {
    const user = userEvent.setup();
    const onPick = vi.fn();
    renderWithProviders(<Harness onPick={onPick} />);

    const none = screen.getByRole('radio', { name: 'field.label_none' });
    expect(none.tabIndex).toBe(0);
    expect(screen.getByRole('radio', { name: 'Client' }).tabIndex).toBe(-1);

    none.focus();
    await user.keyboard('{ArrowRight}');
    expect(onPick).toHaveBeenLastCalledWith('l1');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Client' }));
    expect(screen.getByRole('radio', { name: 'Client' }).tabIndex).toBe(0);

    await user.keyboard('{ArrowLeft}{ArrowLeft}');
    expect(onPick).toHaveBeenLastCalledWith('l2');
  });

  it('disables the chips and says why when the viewer cannot edit', () => {
    renderWithProviders(<Harness canEdit={false} />);
    for (const radio of screen.getAllByRole('radio')) {
      expect((radio as HTMLButtonElement).disabled).toBe(true);
    }
    expect(screen.getByText('field.label_disabled')).toBeTruthy();
  });

  it('shows the error under the field', () => {
    renderWithProviders(<Harness error="validation.label_unknown" />);
    expect(screen.getByText('validation.label_unknown')).toBeTruthy();
  });
});
