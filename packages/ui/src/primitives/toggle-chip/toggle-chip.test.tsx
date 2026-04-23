import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import { ToggleChip, ToggleChipGroup } from './toggle-chip';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Controlled({ initial = false }: { initial?: boolean }): ReactElement {
  const [on, setOn] = useState<boolean>(initial);
  return (
    <ToggleChip pressed={on} onPressedChange={setOn}>
      Tasks
    </ToggleChip>
  );
}

describe.each(THEMES)('ToggleChip [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders with aria-pressed="false" when unpressed', () => {
    render(
      <ToggleChip pressed={false} onPressedChange={() => undefined}>
        Tasks
      </ToggleChip>,
    );
    const btn = screen.getByRole('button', { name: 'Tasks' });
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    expect(btn.getAttribute('data-pressed')).toBe('false');
  });

  it('renders with aria-pressed="true" when pressed', () => {
    render(
      <ToggleChip pressed={true} onPressedChange={() => undefined}>
        Tasks
      </ToggleChip>,
    );
    const btn = screen.getByRole('button', { name: 'Tasks' });
    expect(btn.getAttribute('aria-pressed')).toBe('true');
    expect(btn.getAttribute('data-pressed')).toBe('true');
  });

  it('fires onPressedChange(true) on click when unpressed', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <ToggleChip pressed={false} onPressedChange={fn}>
        Tasks
      </ToggleChip>,
    );
    await user.click(screen.getByRole('button', { name: 'Tasks' }));
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(true);
  });

  it('fires onPressedChange(false) on click when pressed', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <ToggleChip pressed={true} onPressedChange={fn}>
        Tasks
      </ToggleChip>,
    );
    await user.click(screen.getByRole('button', { name: 'Tasks' }));
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(false);
  });

  it('toggles across clicks when driven by a controlled parent', async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const btn = screen.getByRole('button', { name: 'Tasks' });
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    await user.click(btn);
    expect(btn.getAttribute('aria-pressed')).toBe('true');
    await user.click(btn);
    expect(btn.getAttribute('aria-pressed')).toBe('false');
  });

  it('activates via Space and Enter', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <ToggleChip pressed={false} onPressedChange={fn}>
        Tasks
      </ToggleChip>,
    );
    const btn = screen.getByRole('button', { name: 'Tasks' });
    btn.focus();
    await user.keyboard('{Enter}');
    await user.keyboard(' ');
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it('does not fire onPressedChange when disabled', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <ToggleChip pressed={false} onPressedChange={fn} disabled>
        Tasks
      </ToggleChip>,
    );
    await user.click(screen.getByRole('button', { name: 'Tasks' }));
    expect(fn).not.toHaveBeenCalled();
  });

  it('maps the `color` prop to the --chip-color custom property and renders a dot', () => {
    render(
      <ToggleChip pressed={false} onPressedChange={() => undefined} color="var(--nf-color-accent)">
        Tasks
      </ToggleChip>,
    );
    const btn = screen.getByRole('button', { name: 'Tasks' });
    expect(btn.style.getPropertyValue('--chip-color')).toBe('var(--nf-color-accent)');
    // dot is aria-hidden, so query by test pattern: first child span with aria-hidden
    const hidden = btn.querySelector('[aria-hidden="true"]');
    expect(hidden).not.toBeNull();
  });

  it('respects an explicit `label` over string children', () => {
    render(
      <ToggleChip pressed={false} onPressedChange={() => undefined} label="explicit">
        Tasks
      </ToggleChip>,
    );
    expect(screen.getByRole('button', { name: 'explicit' })).toBeDefined();
  });

  it('has no a11y violations (single chip)', async () => {
    const { container } = render(
      <ToggleChip pressed={true} onPressedChange={() => undefined} color="var(--nf-color-accent)">
        Tasks
      </ToggleChip>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe.each(THEMES)('ToggleChipGroup [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders a role="group" wrapper with the supplied aria-label', () => {
    render(
      <ToggleChipGroup label="Layers">
        <ToggleChip pressed={true} onPressedChange={() => undefined}>
          Tasks
        </ToggleChip>
        <ToggleChip pressed={false} onPressedChange={() => undefined}>
          Events
        </ToggleChip>
      </ToggleChipGroup>,
    );
    expect(screen.getByRole('group', { name: 'Layers' })).toBeDefined();
  });

  it('has no a11y violations', async () => {
    const { container } = render(
      <ToggleChipGroup label="Layers">
        <ToggleChip pressed={true} onPressedChange={() => undefined} color="var(--nf-color-accent)">
          Tasks
        </ToggleChip>
        <ToggleChip pressed={false} onPressedChange={() => undefined} color="var(--nf-color-info)">
          Events
        </ToggleChip>
        <ToggleChip
          pressed={false}
          onPressedChange={() => undefined}
          color="var(--nf-color-warning)"
        >
          Blocks
        </ToggleChip>
      </ToggleChipGroup>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });
});
