/**
 * Grants editor on an agent card: admins change five operations and save
 * once (the PATCH replaces the grants wholesale); everyone else sees the
 * same layout with the controls disabled.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  patch: vi.fn(),
  toastShow: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { PATCH: mocks.patch },
  authSdk: { PATCH: mocks.patch },
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: mocks.toastShow },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import AgentToolsEditor from '../agent-tools-editor';

function renderEditor(tools: Record<string, string> | null, canEdit: boolean): void {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <AgentToolsEditor workspaceId="ws-1" agentId="agent-1" tools={tools} canEdit={canEdit} />
    </QueryClientProvider>,
  );
}

async function expand(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole('button', { name: /agents\.tools\.title/ }));
}

beforeEach(() => {
  mocks.patch.mockReset().mockResolvedValue({
    data: { tools: { add_comment: 'approval' } },
    error: null,
    response: new Response(null, { status: 200 }),
  });
  mocks.toastShow.mockReset();
});

describe('AgentToolsEditor', () => {
  it('flags an agent with no grants in the collapsed row and again inside', async () => {
    const user = userEvent.setup();
    renderEditor({}, true);

    expect(screen.getByText('agents.tools.inactive')).toBeDefined();
    await expand(user);
    expect(screen.getByText('agents.tools.emptyWarning')).toBeDefined();
  });

  it('saves the whole grant set, leaving "off" operations out', async () => {
    const user = userEvent.setup();
    renderEditor({ set_priority: 'auto' }, true);
    await expand(user);

    const save = screen.getByRole('button', { name: 'agents.tools.save' });
    expect((save as HTMLButtonElement).disabled).toBe(true);

    const group = screen.getByRole('radiogroup', {
      name: 'agents.tools.operations.add_comment.label',
    });
    await user.click(within(group).getByRole('radio', { name: 'agents.tools.modes.approval' }));
    expect((save as HTMLButtonElement).disabled).toBe(false);
    await user.click(save);

    expect(mocks.patch).toHaveBeenCalledTimes(1);
    const [path, init] = mocks.patch.mock.calls[0] as [string, { params: unknown; body: unknown }];
    expect(path).toBe('/workspaces/{wsId}/ai/agents/{agentId}/tools');
    expect(init.params).toEqual({ path: { wsId: 'ws-1', agentId: 'agent-1' } });
    expect(init.body).toEqual({ tools: { add_comment: 'approval', set_priority: 'auto' } });
  });

  it('shows the same rows disabled, with no save, to a non-admin', async () => {
    const user = userEvent.setup();
    renderEditor({ add_comment: 'approval' }, false);
    await expand(user);

    const group = screen.getByRole('radiogroup', {
      name: 'agents.tools.operations.add_comment.label',
    });
    expect(group.getAttribute('aria-disabled')).toBe('true');
    expect(screen.queryByRole('button', { name: 'agents.tools.save' })).toBeNull();
    expect(screen.getByText('agents.tools.readOnly')).toBeDefined();
  });
});
