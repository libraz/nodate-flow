import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import SegmentedControl, { type SegmentedControlOption } from './segmented-control';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

type Priority = 'none' | 'low' | 'medium' | 'high' | 'urgent';

const PRIORITY_OPTIONS: SegmentedControlOption<Priority>[] = [
  { value: 'none', label: 'None' },
  { value: 'low', label: 'Low', tone: 'info' },
  { value: 'medium', label: 'Medium', tone: 'success' },
  { value: 'high', label: 'High', tone: 'warning' },
  { value: 'urgent', label: 'Urgent', tone: 'danger' },
];

type View = 'month' | 'week' | 'day';

const VIEW_OPTIONS: SegmentedControlOption<View>[] = [
  { value: 'month', label: 'Month' },
  { value: 'week', label: 'Week' },
  { value: 'day', label: 'Day' },
];

/**
 * Controlled wrapper used by the click / keyboard tests so state actually
 * updates between events.
 */
function Controlled<T extends string>({
  initial,
  options,
  colourful = false,
}: {
  initial: T;
  options: SegmentedControlOption<T>[];
  colourful?: boolean;
}): ReactElement {
  const [value, setValue] = useState<T>(initial);
  return (
    <SegmentedControl<T>
      value={value}
      onChange={setValue}
      options={options}
      ariaLabel="Priority"
      colourful={colourful}
    />
  );
}

describe.each(THEMES)('SegmentedControl [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders role="radiogroup" with the accessible label', () => {
    render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
      />,
    );
    expect(screen.getByRole('radiogroup', { name: 'View' })).toBeDefined();
  });

  it('renders a radio button per option with aria-checked reflecting value', () => {
    render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
      />,
    );
    const month = screen.getByRole('radio', { name: 'Month' });
    const week = screen.getByRole('radio', { name: 'Week' });
    const day = screen.getByRole('radio', { name: 'Day' });
    expect(month.getAttribute('aria-checked')).toBe('false');
    expect(week.getAttribute('aria-checked')).toBe('true');
    expect(day.getAttribute('aria-checked')).toBe('false');
  });

  it('uses roving tabindex — only the active segment has tabindex=0', () => {
    render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
      />,
    );
    expect(screen.getByRole('radio', { name: 'Month' }).getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('radio', { name: 'Week' }).getAttribute('tabindex')).toBe('0');
    expect(screen.getByRole('radio', { name: 'Day' }).getAttribute('tabindex')).toBe('-1');
  });

  it('fires onChange(next) on click', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <SegmentedControl<View> value="week" onChange={fn} options={VIEW_OPTIONS} ariaLabel="View" />,
    );
    await user.click(screen.getByRole('radio', { name: 'Day' }));
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith('day');
  });

  it('does not fire onChange when clicking the already-active segment', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <SegmentedControl<View> value="week" onChange={fn} options={VIEW_OPTIONS} ariaLabel="View" />,
    );
    await user.click(screen.getByRole('radio', { name: 'Week' }));
    expect(fn).not.toHaveBeenCalled();
  });

  it('toggles value across clicks when driven by a controlled parent', async () => {
    const user = userEvent.setup();
    render(<Controlled initial="week" options={VIEW_OPTIONS} />);
    const month = screen.getByRole('radio', { name: 'Month' });
    await user.click(month);
    expect(month.getAttribute('aria-checked')).toBe('true');
    expect(month.getAttribute('tabindex')).toBe('0');
  });

  it('navigates with ArrowRight (wraps) and ArrowLeft', async () => {
    const user = userEvent.setup();
    render(<Controlled initial="month" options={VIEW_OPTIONS} />);

    const month = screen.getByRole('radio', { name: 'Month' });
    const week = screen.getByRole('radio', { name: 'Week' });
    const day = screen.getByRole('radio', { name: 'Day' });

    month.focus();
    await user.keyboard('{ArrowRight}');
    expect(week.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(week);

    await user.keyboard('{ArrowRight}');
    expect(day.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(day);

    // Wrap from the last segment back to the first.
    await user.keyboard('{ArrowRight}');
    expect(month.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(month);

    // Wrap from the first segment back to the last.
    await user.keyboard('{ArrowLeft}');
    expect(day.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(day);
  });

  it('supports Home / End', async () => {
    const user = userEvent.setup();
    render(<Controlled initial="week" options={VIEW_OPTIONS} />);
    const week = screen.getByRole('radio', { name: 'Week' });
    week.focus();
    await user.keyboard('{End}');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Day' }));
    expect(screen.getByRole('radio', { name: 'Day' }).getAttribute('aria-checked')).toBe('true');
    await user.keyboard('{Home}');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Month' }));
    expect(screen.getByRole('radio', { name: 'Month' }).getAttribute('aria-checked')).toBe('true');
  });

  it('activates the focused segment on Space and Enter', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <SegmentedControl<View> value="week" onChange={fn} options={VIEW_OPTIONS} ariaLabel="View" />,
    );
    // Focus a non-active segment directly, then activate via keyboard.
    const day = screen.getByRole('radio', { name: 'Day' });
    day.focus();
    await user.keyboard(' ');
    expect(fn).toHaveBeenCalledWith('day');
    fn.mockClear();
    await user.keyboard('{Enter}');
    expect(fn).toHaveBeenCalledWith('day');
  });

  it('skips disabled segments during keyboard navigation', async () => {
    const user = userEvent.setup();
    const options: SegmentedControlOption<View>[] = [
      { value: 'month', label: 'Month' },
      { value: 'week', label: 'Week', disabled: true },
      { value: 'day', label: 'Day' },
    ];
    render(<Controlled initial="month" options={options} />);
    const month = screen.getByRole('radio', { name: 'Month' });
    const week = screen.getByRole('radio', { name: 'Week' });
    const day = screen.getByRole('radio', { name: 'Day' });

    expect(week).toHaveProperty('disabled', true);
    month.focus();
    await user.keyboard('{ArrowRight}');
    // Should skip Week and land on Day.
    expect(document.activeElement).toBe(day);
    expect(day.getAttribute('aria-checked')).toBe('true');
  });

  it('does not fire onChange when a disabled segment is clicked', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    const options: SegmentedControlOption<View>[] = [
      { value: 'month', label: 'Month' },
      { value: 'week', label: 'Week', disabled: true },
      { value: 'day', label: 'Day' },
    ];
    render(
      <SegmentedControl<View> value="month" onChange={fn} options={options} ariaLabel="View" />,
    );
    const week = screen.getByRole('radio', { name: 'Week' });
    // Disabled native buttons swallow clicks entirely.
    await user.click(week);
    expect(fn).not.toHaveBeenCalled();
  });

  it('disables all segments when the group `disabled` prop is true', () => {
    render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
        disabled
      />,
    );
    for (const name of ['Month', 'Week', 'Day']) {
      expect(screen.getByRole('radio', { name }).hasAttribute('disabled')).toBe(true);
    }
  });

  it('applies per-segment tone data attributes regardless of colourful', () => {
    render(
      <SegmentedControl<Priority>
        value="medium"
        onChange={() => undefined}
        options={PRIORITY_OPTIONS}
        ariaLabel="Priority"
      />,
    );
    expect(screen.getByRole('radio', { name: 'None' }).getAttribute('data-tone')).toBe('neutral');
    expect(screen.getByRole('radio', { name: 'Low' }).getAttribute('data-tone')).toBe('info');
    expect(screen.getByRole('radio', { name: 'Medium' }).getAttribute('data-tone')).toBe('success');
    expect(screen.getByRole('radio', { name: 'High' }).getAttribute('data-tone')).toBe('warning');
    expect(screen.getByRole('radio', { name: 'Urgent' }).getAttribute('data-tone')).toBe('danger');
  });

  it('applies per-tone classes in colourful mode', () => {
    const { container } = render(
      <SegmentedControl<Priority>
        value="medium"
        onChange={() => undefined}
        options={PRIORITY_OPTIONS}
        ariaLabel="Priority"
        colourful
      />,
    );
    const root = container.querySelector('[role="radiogroup"]');
    expect(root?.getAttribute('data-colourful')).toBe('true');

    // Colourful mode must apply SOME tone-specific class name to each
    // segment beyond the base .segment class (the exact CSS-module hash is
    // environment-dependent, so we assert on class-count + presence).
    for (const name of ['None', 'Low', 'Medium', 'High', 'Urgent']) {
      const node = screen.getByRole('radio', { name });
      const classes = node.className.split(/\s+/).filter(Boolean);
      // base + segmentColourful + one toneX class (active gets segmentActive too).
      expect(classes.length).toBeGreaterThanOrEqual(3);
    }
  });

  it('does not apply tone classes when colourful is false', () => {
    const { container } = render(
      <SegmentedControl<Priority>
        value="medium"
        onChange={() => undefined}
        options={PRIORITY_OPTIONS}
        ariaLabel="Priority"
      />,
    );
    const root = container.querySelector('[role="radiogroup"]');
    expect(root?.getAttribute('data-colourful')).toBeNull();
  });

  it('uses the ariaLabel override for icon-only segments', () => {
    const options: SegmentedControlOption<View>[] = [
      { value: 'month', label: <span aria-hidden="true">M</span>, ariaLabel: 'Month view' },
      { value: 'week', label: <span aria-hidden="true">W</span>, ariaLabel: 'Week view' },
      { value: 'day', label: <span aria-hidden="true">D</span>, ariaLabel: 'Day view' },
    ];
    render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={options}
        ariaLabel="View"
      />,
    );
    expect(screen.getByRole('radio', { name: 'Month view' })).toBeDefined();
    expect(screen.getByRole('radio', { name: 'Week view' })).toBeDefined();
    expect(screen.getByRole('radio', { name: 'Day view' })).toBeDefined();
  });

  it('forwards className and style to the root', () => {
    const { container } = render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
        className="custom-sc"
        style={{ inlineSize: '320px' }}
      />,
    );
    const root = container.querySelector('[role="radiogroup"]') as HTMLElement | null;
    expect(root).not.toBeNull();
    expect(root?.className).toMatch(/custom-sc/);
    expect(root?.style.inlineSize).toBe('320px');
  });

  it('has no a11y violations (plain, colourful, disabled)', async () => {
    const plain = render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
      />,
    );
    expect(await axe(plain.container)).toHaveNoViolations();
    plain.unmount();

    const colourful = render(
      <SegmentedControl<Priority>
        value="medium"
        onChange={() => undefined}
        options={PRIORITY_OPTIONS}
        ariaLabel="Priority"
        colourful
      />,
    );
    expect(await axe(colourful.container)).toHaveNoViolations();
    colourful.unmount();

    const disabled = render(
      <SegmentedControl<View>
        value="week"
        onChange={() => undefined}
        options={VIEW_OPTIONS}
        ariaLabel="View"
        disabled
      />,
    );
    expect(await axe(disabled.container)).toHaveNoViolations();
  });
});
