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

describe('Tabs sliding indicator', () => {
  /** Lay tabs out in a row, each 10px per label character, as a browser would. */
  function stubTabLayout(): void {
    const width = (el: HTMLElement): number =>
      el.getAttribute('role') === 'tab' ? (el.textContent?.length ?? 0) * 10 : 0;
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return width(this);
    });
    vi.spyOn(HTMLElement.prototype, 'offsetLeft', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      let left = 0;
      for (let el = this.previousElementSibling; el; el = el.previousElementSibling) {
        left += width(el as HTMLElement);
      }
      return left;
    });
  }

  function indicator(container: HTMLElement): HTMLElement {
    const el = container.querySelector<HTMLElement>('[role="tablist"] > span[aria-hidden]');
    if (!el) throw new Error('indicator not rendered');
    return el;
  }

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('follows the active tab when a tab is inserted before it', async () => {
    stubTabLayout();
    const { container, rerender } = render(<Tabs items={ITEMS} value="b" aria-label="demo" />);
    expect(indicator(container).style.insetInlineStart).toBe('50px');

    const inserted: TabItem = { value: 'z', label: 'Zu', content: <p>panel-z</p> };
    rerender(<Tabs items={[inserted, ...ITEMS]} value="b" aria-label="demo" />);
    await vi.waitFor(() => expect(indicator(container).style.insetInlineStart).toBe('70px'));
  });

  it('follows the active tab when a sibling label changes width', async () => {
    stubTabLayout();
    const { container, rerender } = render(<Tabs items={ITEMS} value="b" aria-label="demo" />);
    expect(indicator(container).style.insetInlineStart).toBe('50px');

    const relabelled = ITEMS.map((it) => (it.value === 'a' ? { ...it, label: 'Alphabet' } : it));
    rerender(<Tabs items={relabelled} value="b" aria-label="demo" />);
    await vi.waitFor(() => expect(indicator(container).style.insetInlineStart).toBe('80px'));
    expect(indicator(container).style.inlineSize).toBe('50px');
  });
});
