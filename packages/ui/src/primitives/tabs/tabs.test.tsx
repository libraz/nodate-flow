import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Tabs, { type TabItem } from './tabs';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

const ITEMS: TabItem[] = [
  { value: 'a', label: 'Alpha', content: <p>panel-a</p> },
  { value: 'b', label: 'Bravo', content: <p>panel-b</p> },
  { value: 'c', label: 'Charlie', content: <p>panel-c</p> },
];

describe.each(THEMES)('Tabs [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders tabs and panel with no a11y violations', async () => {
    const { container } = render(<Tabs items={ITEMS} aria-label="demo" />);
    expect(screen.getByRole('tab', { name: 'Alpha' })).toBeDefined();
    expect(screen.getByText('panel-a')).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('switches panel on click (uncontrolled)', async () => {
    const user = userEvent.setup();
    render(<Tabs items={ITEMS} aria-label="demo" />);
    await user.click(screen.getByRole('tab', { name: 'Bravo' }));
    expect(screen.getByText('panel-b')).toBeDefined();
  });

  it('supports controlled mode', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Tabs items={ITEMS} aria-label="demo" value="a" onChange={fn} />);
    await user.click(screen.getByRole('tab', { name: 'Charlie' }));
    expect(fn).toHaveBeenCalledWith('c');
  });

  it('navigates with ArrowRight / ArrowLeft / Home / End', async () => {
    const user = userEvent.setup();
    render(<Tabs items={ITEMS} aria-label="demo" />);
    const first = screen.getByRole('tab', { name: 'Alpha' });
    first.focus();
    await user.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Bravo' }));
    await user.keyboard('{End}');
    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Charlie' }));
    await user.keyboard('{Home}');
    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Alpha' }));
    await user.keyboard('{ArrowLeft}');
    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Charlie' }));
  });

  it('keeps disabled tabs in the tablist and skips them during keyboard navigation', async () => {
    const user = userEvent.setup();
    render(
      <Tabs
        items={[
          ITEMS[0] as TabItem,
          { ...(ITEMS[1] as TabItem), disabled: true },
          ITEMS[2] as TabItem,
        ]}
        aria-label="demo"
      />,
    );

    const disabled = screen.getByRole('tab', { name: 'Bravo' });
    expect(disabled.getAttribute('aria-disabled')).toBe('true');

    const first = screen.getByRole('tab', { name: 'Alpha' });
    first.focus();
    await user.keyboard('{ArrowRight}');

    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Charlie' }));
  });

  it('uses RTL-aware horizontal arrow navigation', async () => {
    const user = userEvent.setup();
    const { container } = render(
      <div dir="rtl">
        <Tabs items={ITEMS} aria-label="demo" />
      </div>,
    );

    const first = screen.getByRole('tab', { name: 'Alpha' });
    first.focus();
    await user.keyboard('{ArrowRight}');

    expect(document.activeElement).toBe(screen.getByRole('tab', { name: 'Charlie' }));
    expect(await axe(container)).toHaveNoViolations();
  });

  it('uses roving tabindex', () => {
    render(<Tabs items={ITEMS} aria-label="demo" defaultValue="b" />);
    expect(screen.getByRole('tab', { name: 'Alpha' }).getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('tab', { name: 'Bravo' }).getAttribute('tabindex')).toBe('0');
  });

  it('makes the active tabpanel focusable', () => {
    render(<Tabs items={ITEMS} aria-label="demo" defaultValue="b" />);
    const panel = screen.getByRole('tabpanel', { name: 'Bravo' });
    expect(panel.getAttribute('tabindex')).toBe('0');
  });
});
