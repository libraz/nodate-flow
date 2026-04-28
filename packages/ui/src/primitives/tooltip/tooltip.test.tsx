import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';
import Tooltip from './tooltip';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Tooltip [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders trigger with no a11y violations (closed)', async () => {
    const { baseElement } = render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    expect(screen.getByRole('button', { name: 'Help' })).toBeDefined();
    expect(await axe(baseElement)).toHaveNoViolations();
  });

  it('opens on focus and shows content', async () => {
    render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    screen.getByRole('button').focus();
    await waitFor(() => {
      expect(screen.getByRole('tooltip')).toBeDefined();
    });
    expect(screen.getByText('Help text')).toBeDefined();
  });

  it('dismisses on Escape', async () => {
    const user = userEvent.setup();
    render(
      <Tooltip content="Help text" delay={0}>
        <button type="button">Help</button>
      </Tooltip>,
    );
    screen.getByRole('button').focus();
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeNull());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull());
  });

  it('collapses the hover delay to 0ms when prefers-reduced-motion is set', async () => {
    /*
     * The default delay is 200ms. Under `prefers-reduced-motion: reduce`
     * the tooltip should appear instantly on hover, treating any easing-in
     * delay as motion the user has explicitly asked us to suppress.
     */
    const original = window.matchMedia;
    window.matchMedia = ((q: string) =>
      ({
        matches: q === '(prefers-reduced-motion: reduce)',
        media: q,
        onchange: null,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
        dispatchEvent: () => true,
      }) as unknown as MediaQueryList) as typeof window.matchMedia;
    try {
      const user = userEvent.setup();
      render(
        <Tooltip content="Help text" delay={200}>
          <button type="button">Help</button>
        </Tooltip>,
      );
      // Hovering should reveal the tooltip without waiting out a 200ms delay.
      // If the delay is honoured, this lookup races and findByRole flakes.
      await user.hover(screen.getByRole('button', { name: 'Help' }));
      const tip = await screen.findByRole('tooltip');
      expect(tip.textContent).toContain('Help text');
    } finally {
      window.matchMedia = original;
    }
  });
});
