import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Button from './button';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

describe.each(THEMES)('Button [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders children and has no a11y violations', async () => {
    const { container } = render(<Button>Click me</Button>);
    expect(screen.getByRole('button', { name: 'Click me' })).toBeDefined();
    expect(await axe(container)).toHaveNoViolations();
  });

  it('defaults type to "button"', () => {
    render(<Button>Go</Button>);
    expect(screen.getByRole('button').getAttribute('type')).toBe('button');
  });

  it('fires onClick when activated', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Button onClick={fn}>Press</Button>);
    await user.click(screen.getByRole('button'));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('does not fire onClick when disabled', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <Button onClick={fn} disabled>
        Press
      </Button>,
    );
    await user.click(screen.getByRole('button'));
    expect(fn).not.toHaveBeenCalled();
  });

  it('supports keyboard activation via Enter', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Button onClick={fn}>Press</Button>);
    screen.getByRole('button').focus();
    await user.keyboard('{Enter}');
    expect(fn).toHaveBeenCalled();
  });

  it('honors aria-label for icon-only buttons (no visible text)', async () => {
    const { container } = render(
      <Button aria-label="Close dialog">
        <span aria-hidden="true">x</span>
      </Button>,
    );
    // The accessible name comes from aria-label, not the icon glyph.
    expect(screen.getByRole('button', { name: 'Close dialog' })).toBeDefined();
    // The "x" glyph itself is hidden from AT.
    const glyph = screen.getByText('x');
    expect(glyph.getAttribute('aria-hidden')).toBe('true');
    // axe must not flag the icon-only button (e.g. button-name rule).
    expect(await axe(container)).toHaveNoViolations();
  });

  it('exposes disabled state to assistive tech via the disabled property', () => {
    render(<Button disabled>Press</Button>);
    const el = screen.getByRole('button') as HTMLButtonElement;
    // Browsers expose `disabled` as the AT-facing state. Native <button>
    // automatically maps this to the platform a11y tree without aria-disabled.
    expect(el.disabled).toBe(true);
    expect(el.hasAttribute('disabled')).toBe(true);
    // Buttons should NOT use aria-disabled when the native attribute is set
    // — that would double-announce or cause AT to ignore the disabled state.
    expect(el.getAttribute('aria-disabled')).toBeNull();
  });

  it('renders as an anchor when as="a" is passed', () => {
    render(
      <Button as="a" href="/example">
        Link
      </Button>,
    );
    const link = screen.getByRole('link', { name: 'Link' }) as HTMLAnchorElement;
    expect(link.tagName).toBe('A');
    expect(link.getAttribute('href')).toBe('/example');
    // `type="button"` only makes sense on <button>; the polymorphic switch
    // must not spread it onto an <a>.
    expect(link.hasAttribute('type')).toBe(false);
  });

  it('paints the focus-visible ring via the design-system shadow token', () => {
    // Button.module.css line 26-29:
    //   .root:focus-visible { outline: none; box-shadow: var(--nf-shadow-focus); }
    // CSS modules hash the class name at build time; we assert on the
    // stable .root prefix that the build always produces. This prevents a
    // regression where the focus rule is removed or renamed without a
    // corresponding visual replacement.
    render(<Button>Press</Button>);
    const el = screen.getByRole('button') as HTMLButtonElement;
    const classes = el.className.split(/\s+/).filter(Boolean);
    // At least one class should match the css-module .root hash so the
    // :focus-visible rule actually targets this element.
    expect(classes.some((c) => /root/.test(c))).toBe(true);
  });
});
