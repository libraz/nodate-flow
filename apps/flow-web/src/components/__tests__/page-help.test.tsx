/** Component tests for PageHelp. */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import PageHelp, { OPEN_SHORTCUTS_HELP_EVENT } from '../page-help';

const bundles: Record<string, unknown> = {
  'demo.title': 'Demo title',
  'demo.summary': 'Demo summary',
  'demo.tips': { t1: 'First tip', t2: 'Second tip', t3: 'Third tip' },
  'bare.title': 'Bare title',
  'bare.summary': 'Bare summary',
};

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { returnObjects?: boolean; defaultValue?: unknown }) => {
      if (key in bundles) return bundles[key];
      // Mirror i18next: a missing key yields the key, or the default value.
      if (opts?.returnObjects) return key;
      return opts?.defaultValue ?? key;
    },
  }),
}));

afterEach(() => {
  vi.restoreAllMocks();
});

describe('<PageHelp>', () => {
  it('opens from the trigger and closes on Escape, returning focus', async () => {
    const user = userEvent.setup();
    render(<PageHelp topic="demo" />);
    const trigger = screen.getByRole('button', { name: 'help.trigger' });
    expect(trigger.getAttribute('aria-expanded')).toBe('false');

    await user.click(trigger);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByRole('dialog')).not.toBeNull();
    expect(screen.getByRole('heading', { name: 'Demo title' })).not.toBeNull();
    expect(screen.getByText('Demo summary')).not.toBeNull();

    await user.keyboard('{Escape}');
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(trigger);
  });

  it('focuses the labelled panel itself on open, without scrolling', async () => {
    const user = userEvent.setup();
    const focusSpy = vi.spyOn(HTMLElement.prototype, 'focus');
    render(<PageHelp topic="demo" />);
    await user.click(screen.getByRole('button', { name: 'help.trigger' }));
    const panel = screen.getByRole('dialog', { name: 'Demo title' });
    await waitFor(() => expect(document.activeElement).toBe(panel));
    expect(focusSpy.mock.calls.some(([opts]) => opts?.preventScroll === true)).toBe(true);
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'help.shortcuts' }));
  });

  it('renders tips in order', async () => {
    const user = userEvent.setup();
    render(<PageHelp topic="demo" />);
    await user.click(screen.getByRole('button', { name: 'help.trigger' }));
    const items = screen.getAllByRole('listitem').map((li) => li.textContent);
    expect(items).toEqual(['First tip', 'Second tip', 'Third tip']);
  });

  it('tolerates missing tips', async () => {
    const user = userEvent.setup();
    render(<PageHelp topic="bare" />);
    await user.click(screen.getByRole('button', { name: 'help.trigger' }));
    expect(screen.getByText('Bare summary')).not.toBeNull();
    expect(screen.queryByRole('list')).toBeNull();
  });

  it('asks the layout to open shortcuts and closes itself', async () => {
    const user = userEvent.setup();
    const listener = vi.fn();
    window.addEventListener(OPEN_SHORTCUTS_HELP_EVENT, listener);
    render(<PageHelp topic="demo" />);
    const trigger = screen.getByRole('button', { name: 'help.trigger' });
    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'help.shortcuts' }));
    window.removeEventListener(OPEN_SHORTCUTS_HELP_EVENT, listener);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });
});
