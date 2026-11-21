/**
 * AppShell owns the only command palette; the top-bar search, the dock and
 * Cmd/Ctrl+K all open that one instance.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: ({ select }: { select: (s: unknown) => unknown }) =>
    select({ location: { pathname: '/today' } }),
}));

vi.mock('../command-palette', () => ({
  default: ({ open, initialCommandMode }: { open: boolean; initialCommandMode?: boolean }) => (
    <div data-testid="palette-mount">
      {open ? <div role="dialog" data-command-mode={String(Boolean(initialCommandMode))} /> : null}
    </div>
  ),
}));

vi.mock('../sidebar', () => ({ default: () => null }));
vi.mock('../top-bar-breadcrumb', () => ({ default: () => null }));
vi.mock('../../../features/timeboxes/active-timebox-bar', () => ({ default: () => null }));
vi.mock('../../../features/ai-providers/cost-meter', () => ({ default: () => null }));
vi.mock('../../../features/notifications/notification-bell', () => ({ default: () => null }));
vi.mock('../../../lib/use-current-workspace-role', () => ({
  useCurrentWorkspaceRole: () => null,
  isWorkspaceAdmin: () => false,
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import AppShell from '../app-shell';
import { OPEN_COMMAND_PALETTE_EVENT, useCommandPaletteStore } from '../command-palette-store';

function renderShell(): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AppShell>
        <p>page</p>
      </AppShell>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  useCommandPaletteStore.getState().closePalette();
});

describe('command palette ownership', () => {
  it('mounts exactly one palette', () => {
    renderShell();
    expect(screen.getAllByTestId('palette-mount')).toHaveLength(1);
  });

  it('opens one dialog from the top-bar search', async () => {
    renderShell();
    await userEvent.setup().click(document.querySelector('[data-search-trigger]') as Element);
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
  });

  it('opens one dialog in command mode from the dock AI button', async () => {
    renderShell();
    await userEvent.setup().click(screen.getByRole('button', { name: 'dock.ai_assist' }));
    const dialogs = screen.getAllByRole('dialog');
    expect(dialogs).toHaveLength(1);
    expect(dialogs[0]?.getAttribute('data-command-mode')).toBe('true');
  });

  it('opens and toggles one dialog with Cmd+K', async () => {
    renderShell();
    await userEvent.setup().keyboard('{Meta>}k{/Meta}');
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    await userEvent.setup().keyboard('{Meta>}k{/Meta}');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens one dialog from the window event', () => {
    renderShell();
    act(() => {
      window.dispatchEvent(new Event(OPEN_COMMAND_PALETTE_EVENT));
    });
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
  });
});
