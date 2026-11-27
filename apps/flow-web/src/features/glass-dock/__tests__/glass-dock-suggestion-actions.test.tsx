import { toaster } from '@nodate-flow/ui/primitives/toast';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { API_URL, server, useMockApi } from '../../../../tests/msw/server';
import { authStore } from '../../auth/auth-store';
import { useAiPanelStore } from '../ai-panel-store';
import GlassDock from '../glass-dock';

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>();
  return { ...actual, useNavigate: () => navigateMock };
});

vi.mock('../../../lib/use-current-workspace', () => ({
  useCurrentWorkspaceId: () => 'ws-1',
}));

vi.mock('../../realtime/use-workspace-stream', () => ({
  useWorkspaceStream: () => {},
}));

useMockApi();

const WORKSPACE_ID = 'ws-1';
const ACTOR_ID = 'actor-1';
const INBOX_ITEM_ID = 'inbox-1';
const PROPOSAL_ID = 'proposal-1';

interface SuggestionFixture {
  eventId: string;
  proposalId: string;
  inboxItemId: string;
  revision: number;
  sourceVersionStatus: 'captured' | 'legacy';
  proposedAt: number;
  reasoning: string;
  recommendedAction: string;
  score: number;
  decisionState?: string;
  decisionExpiresAt?: number;
}

interface InboxItemFixture {
  id: string;
  createdAt: number;
  receivedAt: number;
  source: string;
  kind: string;
  workspaceId: string;
  taskId?: string;
  taskTitle?: string;
}

interface ExecuteBody {
  operationKey: string;
  proposalId: string;
  expectedRevision: number;
  action: string;
  reasoning: string;
}

function aSuggestion(overrides: Partial<SuggestionFixture> = {}): SuggestionFixture {
  return {
    eventId: PROPOSAL_ID,
    proposalId: PROPOSAL_ID,
    inboxItemId: INBOX_ITEM_ID,
    revision: 0,
    sourceVersionStatus: 'captured',
    proposedAt: 1_700_000_000,
    reasoning: 'This inbox item should be archived.',
    recommendedAction: 'archive',
    score: 0.9,
    ...overrides,
  };
}

function anInboxItem(overrides: Partial<InboxItemFixture> = {}): InboxItemFixture {
  return {
    id: INBOX_ITEM_ID,
    createdAt: 1_700_000_000,
    receivedAt: 1_700_000_000,
    source: 'manual',
    kind: 'task.updated',
    workspaceId: WORKSPACE_ID,
    taskId: 'task-1',
    taskTitle: 'Example task',
    ...overrides,
  };
}

function anInboxItemWithoutTask(): InboxItemFixture {
  const { taskId: _taskId, taskTitle: _taskTitle, ...item } = anInboxItem();
  return item;
}

function installHandlers(
  suggestion: SuggestionFixture,
  item: InboxItemFixture,
  options: { executeStatus?: number; decisionStatus?: number } = {},
): {
  operations: string[];
  executeBodies: ExecuteBody[];
  decisionBodies: Array<Record<string, unknown>>;
  setExecuteStatus: (status: number) => void;
} {
  const operations: string[] = [];
  const executeBodies: ExecuteBody[] = [];
  const decisionBodies: Array<Record<string, unknown>> = [];
  let executeStatus = options.executeStatus ?? 200;
  let active = true;
  let currentSuggestion = { ...suggestion };
  const workspaceBase = `${API_URL}/workspaces/${WORKSPACE_ID}`;

  server.use(
    http.get(`${workspaceBase}/ai/suggestions`, () =>
      HttpResponse.json({ suggestions: active ? [currentSuggestion] : [] }),
    ),
    http.get(`${workspaceBase}/ai/auto-actions`, () => HttpResponse.json({ actions: [] })),
    http.get(`${workspaceBase}/ai/state-suggestions`, () => HttpResponse.json({ suggestions: [] })),
    http.get(`${workspaceBase}/ai/reminders`, () => HttpResponse.json({ reminders: [] })),
    http.get(`${API_URL}/inbox`, () => HttpResponse.json({ items: [item] })),
    http.post(`${workspaceBase}/ai/suggestions/${INBOX_ITEM_ID}/execute`, async ({ request }) => {
      const body = (await request.json()) as ExecuteBody;
      operations.push('execute');
      executeBodies.push(body);
      if (executeStatus >= 400) {
        return HttpResponse.json(
          { detail: 'Suggestion execution failed' },
          { status: executeStatus },
        );
      }
      if (body.action !== 'open') active = false;
      const navigation = body.action === 'open';
      return HttpResponse.json({
        operationId: `operation-${executeBodies.length}`,
        proposalId: PROPOSAL_ID,
        revision: body.expectedRevision,
        action: body.action,
        status: navigation ? 'navigation_ready' : 'executed',
        inboxItemId: INBOX_ITEM_ID,
        ...(navigation && item.taskId ? { taskId: item.taskId } : {}),
        ...(!navigation ? { executedAt: 1_700_000_001 } : {}),
        replayed: false,
      });
    }),
    http.patch(`${workspaceBase}/ai/suggestions/${INBOX_ITEM_ID}/decision`, async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>;
      operations.push('decision');
      decisionBodies.push(body);
      if ((options.decisionStatus ?? 200) >= 400) {
        return HttpResponse.json(
          { detail: 'Suggestion edit failed' },
          { status: options.decisionStatus ?? 500 },
        );
      }
      if (body.state === 'dismissed') active = false;
      currentSuggestion = {
        ...currentSuggestion,
        revision: 1,
        decisionState: 'edited',
        recommendedAction: String(body.action),
        reasoning: String(body.reasoning),
      };
      return HttpResponse.json({
        proposalId: PROPOSAL_ID,
        revision: 1,
        state: body.state,
        action: body.action,
        reasoning: body.reasoning,
        decisionExpiresAt: 1_800_000_000,
      });
    }),
  );

  return {
    operations,
    executeBodies,
    decisionBodies,
    setExecuteStatus: (status) => {
      executeStatus = status;
    },
  };
}

function seedSession(): void {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(
    JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 3600 }),
  ).toString('base64url');
  authStore.getState().setSession(`${header}.${payload}.`, {
    id: ACTOR_ID,
    email: 'actor@example.test',
    displayName: 'Actor',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    themePreference: 'system',
    isInstanceAdmin: false,
    hasPassword: false,
  });
}

async function openDock(recommendedAction = 'archive'): Promise<void> {
  // The glass dock's AI button opens the panel through this store.
  useAiPanelStore.setState({ open: true, returnFocus: false });
  renderWithProviders(<GlassDock />);
  await screen.findByRole('region', { name: 'glass_dock.title' });
  const known = ['open', 'snooze', 'archive'].includes(recommendedAction);
  await screen.findByText(known ? `edit.action.${recommendedAction}` : recommendedAction);
}

async function waitForActionToSettle(): Promise<void> {
  await waitFor(() => {
    const applyButton = screen.queryByRole('button', { name: 'action.apply' });
    expect(applyButton === null || !(applyButton as HTMLButtonElement).disabled).toBe(true);
  });
}

beforeEach(() => {
  seedSession();
  navigateMock.mockReset();
});

afterEach(() => {
  useAiPanelStore.setState({ open: false, returnFocus: false });
  authStore.getState().clearSession();
  vi.restoreAllMocks();
});

describe('GlassDock suggestion actions', () => {
  it('executes archive through the atomic suggestion command', async () => {
    const user = userEvent.setup();
    const { operations, executeBodies } = installHandlers(aSuggestion(), anInboxItem());
    await openDock();

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toEqual(['execute']));
    expect(executeBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 0,
      action: 'archive',
      reasoning: 'This inbox item should be archived.',
    });
    expect(executeBodies[0]?.operationKey).toBeTruthy();
    await waitForActionToSettle();
  });

  it('executes snooze through the atomic suggestion command', async () => {
    const user = userEvent.setup();
    const { operations, executeBodies } = installHandlers(
      aSuggestion({ recommendedAction: 'snooze' }),
      anInboxItem(),
    );
    await openDock('snooze');

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toEqual(['execute']));
    expect(executeBodies[0]).toMatchObject({ action: 'snooze', proposalId: PROPOSAL_ID });
    await waitForActionToSettle();
  });

  it('navigates to a valid task while keeping open pending', async () => {
    const user = userEvent.setup();
    const { operations, executeBodies } = installHandlers(
      aSuggestion({ recommendedAction: 'open' }),
      anInboxItem({ taskId: 'task-open' }),
    );
    navigateMock.mockImplementation(() => Promise.resolve());
    await openDock('open');

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith({
        to: '/tasks/$taskId',
        params: { taskId: 'task-open' },
      }),
    );
    expect(operations).toEqual(['execute']);
    expect(executeBodies[0]?.action).toBe('open');
    await waitForActionToSettle();
    expect(screen.getByRole('button', { name: 'action.apply' })).toBeTruthy();
  });

  it('keeps a failed command pending and reuses the operation key on retry', async () => {
    const user = userEvent.setup();
    const toast = vi.spyOn(toaster, 'show');
    const { operations, executeBodies, setExecuteStatus } = installHandlers(
      aSuggestion(),
      anInboxItem(),
      { executeStatus: 500 },
    );
    await openDock();

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toEqual(['execute']));
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' })),
    );
    await waitForActionToSettle();
    expect(screen.getByRole('button', { name: 'action.apply' })).toBeTruthy();

    setExecuteStatus(200);
    await user.click(screen.getByRole('button', { name: 'action.apply' }));
    await waitFor(() => expect(operations).toEqual(['execute', 'execute']));
    expect(executeBodies[1]?.operationKey).toBe(executeBodies[0]?.operationKey);
    await waitForActionToSettle();
  });

  it('does not send unsupported recommendations to the command API', async () => {
    const user = userEvent.setup();
    const toast = vi.spyOn(toaster, 'show');
    const { operations } = installHandlers(
      aSuggestion({ recommendedAction: 'delete' }),
      anInboxItem(),
    );
    await openDock('delete');

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' })),
    );
    await waitForActionToSettle();
    expect(operations).toEqual([]);
  });

  it('keeps open pending when the server cannot provide a task target', async () => {
    const user = userEvent.setup();
    const toast = vi.spyOn(toaster, 'show');
    const { operations } = installHandlers(
      aSuggestion({ recommendedAction: 'open' }),
      anInboxItemWithoutTask(),
      { executeStatus: 404 },
    );
    await openDock('open');

    await user.click(screen.getByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toEqual(['execute']));
    expect(navigateMock).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' })),
    );
    await waitForActionToSettle();
    expect(screen.getByRole('button', { name: 'action.apply' })).toBeTruthy();
  });

  it('opens the editor and executes the saved action at the new revision', async () => {
    const user = userEvent.setup();
    const { operations, executeBodies, decisionBodies } = installHandlers(
      aSuggestion({ recommendedAction: 'open' }),
      anInboxItem(),
    );
    await openDock('open');

    await user.click(screen.getByRole('button', { name: 'action.edit' }));
    expect(await screen.findByRole('dialog')).toBeTruthy();
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe(
      'This inbox item should be archived.',
    );
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'edit.action_label' }),
      'archive',
    );
    await user.click(screen.getByRole('button', { name: 'edit.save' }));
    await waitFor(() => expect(operations).toEqual(['decision']));
    await user.click(await screen.findByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toEqual(['decision', 'execute']));
    expect(decisionBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 0,
      state: 'edited',
      action: 'archive',
    });
    expect(executeBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 1,
      action: 'archive',
    });
    await waitForActionToSettle();
  });

  it('persists a dismissal before removing the suggestion from the dock', async () => {
    const user = userEvent.setup();
    const { operations, decisionBodies } = installHandlers(aSuggestion(), anInboxItem());
    await openDock();

    await user.click(screen.getByRole('button', { name: 'action.dismiss' }));

    await waitFor(() => expect(operations).toEqual(['decision']));
    expect(decisionBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 0,
      state: 'dismissed',
    });
    await waitFor(() => expect(screen.queryByRole('button', { name: 'action.apply' })).toBeNull());
  });

  it('shows legacy suggestions but disables actions until triage captures a source version', async () => {
    installHandlers(aSuggestion({ sourceVersionStatus: 'legacy' }), anInboxItem());
    await openDock();

    expect(screen.getByRole('note')).toBeTruthy();
    expect(
      (screen.getByRole('button', { name: 'action.apply' }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });
});
