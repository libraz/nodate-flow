/**
 * Theme preferences that survive a reload, and controls that agree with
 * what is on screen.
 *
 * Two defects sit behind these tests, and both are the same shape: the
 * value the user saved and the value the UI shows drift apart.
 *
 *   - Server hydration latched before it had an answer. `fetchServerTheme`
 *     reads the session, which is established below this provider and
 *     therefore later, so the first attempt always came back empty — and
 *     the effect never ran again. A saved `washi-dark` rendered as the
 *     default theme while the profile page, reading the server value
 *     directly, displayed "washi-dark".
 *   - `setFamily` resolved `system` to whichever concrete theme happened
 *     to be showing and wrote that back, pinning the account to that
 *     moment's light/dark, ending OS following, and flipping an untouched
 *     control from "System" to "Light" — then persisting it.
 */

import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { type ThemePreference, ThemeProvider, useThemeContext } from '../theme-provider';

/** Surfaces the whole context so assertions read like the UI does. */
function Probe(): ReactElement {
  const { preference, resolved, family, colorMode, setFamily, setColorMode } = useThemeContext();
  return (
    <div>
      <span data-testid="preference">{preference}</span>
      <span data-testid="resolved">{resolved}</span>
      <span data-testid="family">{family}</span>
      <span data-testid="colorMode">{colorMode}</span>
      <button type="button" onClick={() => setFamily('washi')}>
        washi
      </button>
      <button type="button" onClick={() => setColorMode('dark')}>
        dark
      </button>
      <button type="button" onClick={() => setColorMode('system')}>
        system
      </button>
    </div>
  );
}

function renderProvider(props: {
  fetchServerTheme?: () => Promise<ThemePreference | null>;
  syncServerTheme?: (p: ThemePreference) => Promise<void>;
}): void {
  render(
    <ThemeProvider {...props}>
      <Probe />
    </ThemeProvider>,
  );
}

/** Pretend the OS is in light mode unless a test says otherwise. */
function stubMatchMedia(dark: boolean): void {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: dark,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
      onchange: null,
    }),
  });
}

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  stubMatchMedia(false);
});

describe('server hydration', () => {
  it('applies the stored theme even though the session arrives after mount', async () => {
    // First call: no session yet. This is the normal case, not an edge one.
    let authenticated = false;
    const fetchServerTheme = vi.fn(async () => (authenticated ? 'washi-dark' : null));

    renderProvider({ fetchServerTheme: fetchServerTheme as () => Promise<ThemePreference | null> });

    expect(screen.getByTestId('resolved').textContent).not.toBe('washi-dark');

    authenticated = true;

    await waitFor(() => {
      expect(screen.getByTestId('preference').textContent).toBe('washi-dark');
    });
    // What the DOM actually renders, not just what the context reports.
    expect(document.documentElement.getAttribute('data-theme')).toBe('washi-dark');
    expect(screen.getByTestId('family').textContent).toBe('washi');
    expect(screen.getByTestId('colorMode').textContent).toBe('dark');
  });

  it('stops asking once the server has answered', async () => {
    const fetchServerTheme = vi.fn(async () => 'washi-light' as ThemePreference);
    renderProvider({ fetchServerTheme });

    await waitFor(() => {
      expect(screen.getByTestId('preference').textContent).toBe('washi-light');
    });
    const calls = fetchServerTheme.mock.calls.length;
    await act(async () => {
      await new Promise((r) => setTimeout(r, 400));
    });
    expect(fetchServerTheme.mock.calls.length).toBe(calls);
  });
});

describe('setFamily with the colour mode on system', () => {
  it('keeps the colour mode on system', async () => {
    const user = userEvent.setup();
    renderProvider({});

    expect(screen.getByTestId('colorMode').textContent).toBe('system');

    await user.click(screen.getByRole('button', { name: 'washi' }));

    // The control the user did not touch must not have moved.
    expect(screen.getByTestId('colorMode').textContent).toBe('system');
    expect(screen.getByTestId('preference').textContent).toBe('system');
    // ...and the family they did touch must have.
    expect(screen.getByTestId('family').textContent).toBe('washi');
    expect(document.documentElement.getAttribute('data-theme')).toBe('washi-light');
  });

  it('never persists a concrete theme for a system-mode account', async () => {
    const user = userEvent.setup();
    const syncServerTheme = vi.fn(async (_pref: ThemePreference) => undefined);
    renderProvider({ syncServerTheme });

    // Only a preference change reaches the server, so the account has to
    // be driven through one before the payload can be observed at all.
    // Going dark and back to system is the shortest route that leaves the
    // account in system mode with a sync already on the wire.
    await user.click(screen.getByRole('button', { name: 'dark' }));
    await user.click(screen.getByRole('button', { name: 'system' }));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(syncServerTheme.mock.calls.length).toBeGreaterThan(0);
    expect(syncServerTheme).toHaveBeenLastCalledWith('system');

    // Changing the family while in system mode must not resolve the
    // preference to whichever concrete theme happens to be showing: that
    // would pin the account to this moment's light/dark and end OS
    // following. Nothing new may reach the server, and nothing that did
    // may carry a concrete theme.
    const before = syncServerTheme.mock.calls.length;
    await user.click(screen.getByRole('button', { name: 'washi' }));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(syncServerTheme.mock.calls.length).toBe(before);
    expect(syncServerTheme.mock.calls.map((call) => call[0])).not.toContain('light');
    expect(syncServerTheme.mock.calls.map((call) => call[0])).not.toContain('dark-washi');

    expect(window.localStorage.getItem('nf.theme')).toBe('system');
    expect(window.localStorage.getItem('nf.theme.family')).toBe('washi');
  });

  it('still follows the OS after the family changed', async () => {
    const user = userEvent.setup();
    stubMatchMedia(true); // OS is dark
    renderProvider({});

    await user.click(screen.getByRole('button', { name: 'washi' }));

    expect(screen.getByTestId('resolved').textContent).toBe('washi-dark');
    expect(screen.getByTestId('colorMode').textContent).toBe('system');
  });

  it('remembers the family across a reload while on system', async () => {
    const user = userEvent.setup();
    renderProvider({});
    await user.click(screen.getByRole('button', { name: 'washi' }));

    // Same storage, fresh provider — what a reload looks like.
    cleanup();
    renderProvider({});

    expect(screen.getByTestId('family').textContent).toBe('washi');
    expect(screen.getByTestId('colorMode').textContent).toBe('system');
  });

  it('switching back to system resumes in the chosen family', async () => {
    const user = userEvent.setup();
    renderProvider({});

    await user.click(screen.getByRole('button', { name: 'washi' }));
    await user.click(screen.getByRole('button', { name: 'dark' }));
    expect(screen.getByTestId('preference').textContent).toBe('washi-dark');

    await user.click(screen.getByRole('button', { name: 'system' }));
    expect(screen.getByTestId('colorMode').textContent).toBe('system');
    expect(screen.getByTestId('family').textContent).toBe('washi');
  });
});
