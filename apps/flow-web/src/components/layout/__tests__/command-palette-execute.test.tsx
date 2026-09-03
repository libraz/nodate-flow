/**
 * Enter in NL command mode has to do what the palette says it will do.
 *
 * The palette bills an LLM call to resolve a command, then renders "press
 * Enter to execute". It used to answer that Enter by navigating home for
 * every tool it had no case for, which is indistinguishable from success:
 * the dialog closes, the app moves, and nothing happened.
 *
 * So: a wired tool must reach its REST call, and an unwired one must stay
 * put and say it cannot run.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  navigate: vi.fn(),
  resolve: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: mocks.get, POST: mocks.post, PATCH: mocks.patch },

  authSdk: { GET: mocks.get, POST: mocks.post, PATCH: mocks.patch },
}));

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>();
  return { ...actual, useNavigate: () => mocks.navigate };
});

vi.mock('../../../features/workspaces/api', () => ({
  useWorkspacesQuery: () => ({
    data: [{ id: 'ws-1', name: 'Workspace' }],
    response: new Response(null, { status: 200 }),
  }),
  response: new Response(null, { status: 200 }),
}));

vi.mock('../../../lib/use-current-workspace', () => ({
  useCurrentWorkspaceId: () => 'ws-1',
}));

// The resolver is the metered LLM boundary; the dispatch it feeds is real.
vi.mock('../../../features/nl-command/api', () => ({
  useResolveCommand: () => ({
    mutate: (prompt: string, opts: { onSuccess: (data: unknown) => void }) => {
      opts.onSuccess(mocks.resolve(prompt));
    },
    isPending: false,
    isError: false,
  }),
}));

import CommandPalette from '../command-palette';

const TASK_ID = '0195b1a0-1111-7000-8000-000000000001';

function openPalette(): HTMLInputElement {
  renderWithProviders(<CommandPalette open onClose={() => {}} initialCommandMode />);
  return screen.getByRole('textbox') as HTMLInputElement;
}

/**
 * Types a command and presses Enter twice: once to resolve (waiting for
 * the resolved tool name to appear), once to execute.
 */
async function runCommand(input: HTMLInputElement, prompt: string, tool: string): Promise<void> {
  fireEvent.change(input, { target: { value: `> ${prompt}` } });
  fireEvent.keyDown(input, { key: 'Enter' });
  await screen.findByText(tool);
  fireEvent.keyDown(input, { key: 'Enter' });
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.post.mockReset();
  mocks.patch.mockReset();
  mocks.navigate.mockReset();
  mocks.resolve.mockReset();
});

describe('NL command execution', () => {
  it('runs the resolved transition against the API', async () => {
    mocks.resolve.mockReturnValue({
      tool: 'transition_task',
      args: { taskId: 'Login bug', transition: 'complete' },
      confidence: 0.92,
    });
    mocks.get.mockResolvedValue({
      data: { tasks: [{ id: TASK_ID, title: 'Login bug' }], total: 1 },
      error: null,
      response: new Response(null, { status: 200 }),
    });
    mocks.post.mockResolvedValue({
      data: { ok: true },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const input = openPalette();
    await runCommand(input, 'mark the login bug done', 'transition_task');

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/tasks/{id}/transitions', {
        params: { path: { id: TASK_ID } },
        body: { transition: 'complete' },
      });
    });
    await waitFor(() => {
      expect(mocks.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: `/tasks/${TASK_ID}` }),
      );
    });
  });

  it('creates the task for create_task rather than deep-linking a form', async () => {
    mocks.resolve.mockReturnValue({
      tool: 'create_task',
      args: { title: 'Write the changelog' },
      confidence: 0.9,
    });
    mocks.get.mockResolvedValue({
      data: { projects: [{ id: 'prj-1', name: 'Platform' }] },
      error: null,
      response: new Response(null, { status: 200 }),
    });
    mocks.post.mockResolvedValue({
      data: { id: TASK_ID },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const input = openPalette();
    await runCommand(input, 'add a task to write the changelog', 'create_task');

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalledWith('/tasks', {
        body: { projectId: 'prj-1', title: 'Write the changelog', visibility: 'public' },
      });
    });
    await waitFor(() => {
      expect(mocks.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: `/tasks/${TASK_ID}` }),
      );
    });
  });

  // The regression this file exists for.
  it('does not navigate home for a tool it cannot run', async () => {
    mocks.resolve.mockReturnValue({
      tool: 'smart_create_task',
      args: { prompt: 'plan the offsite' },
      confidence: 0.95,
    });

    const input = openPalette();
    await runCommand(input, 'plan the offsite', 'smart_create_task');

    expect(await screen.findByText('dock.command_palette.unsupported')).toBeTruthy();
    await waitFor(() => {
      expect(mocks.navigate).not.toHaveBeenCalled();
    });
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(screen.queryByText('dock.command_palette.confirm_execute')).toBeNull();
  });

  it('stays put and explains when the command names nothing that exists', async () => {
    mocks.resolve.mockReturnValue({
      tool: 'transition_task',
      args: { taskId: 'a task that is not there', transition: 'complete' },
      confidence: 0.88,
    });
    mocks.get.mockResolvedValue({
      data: { tasks: [], total: 0 },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    const input = openPalette();
    await runCommand(input, 'close the missing task', 'transition_task');

    expect(await screen.findByText('dock.command_palette.unresolved_not_found')).toBeTruthy();
    expect(mocks.navigate).not.toHaveBeenCalled();
    expect(mocks.post).not.toHaveBeenCalled();
  });
});
