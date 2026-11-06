import { toaster } from '@nodate-flow/ui/primitives/toast';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { HttpResponse, http } from 'msw';
import { Suspense } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { API_URL, server, useMockApi } from '../../../../tests/msw/server';
import { type Suggestion, suggestionsStore } from '../../ai-suggestions/store';
import { authStore } from '../../auth/auth-store';
import InboxList from '../inbox-list';

vi.mock('../../workspaces/api', () => ({
  useWorkspacesQuery: () => ({ data: [{ id: 'ws-1', name: 'Workspace' }] }),
}));

useMockApi();

const WORKSPACE_ID = 'ws-1';
const ACTOR_ID = 'actor-1';
const PROPOSAL_ID = 'proposal-1';
const INBOX_ITEM_ID = 'inbox-1';

const SUGGESTION: Suggestion = {
  eventId: PROPOSAL_ID,
  proposalId: PROPOSAL_ID,
  inboxItemId: INBOX_ITEM_ID,
  revision: 0,
  sourceVersionStatus: 'captured',
  proposedAt: 1_700_000_000,
  recommendedAction: 'open',
  reasoning: 'Open the task linked to this inbox item.',
  score: 0.9,
  workspaceId: WORKSPACE_ID,
  actorId: ACTOR_ID,
};

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

function installHandlers(
  executeStatus = 200,
  decisionStatus = 200,
): {
  operations: string[];
  executeBodies: Array<Record<string, unknown>>;
  decisionBodies: Array<Record<string, unknown>>;
} {
  const operations: string[] = [];
  const executeBodies: Array<Record<string, unknown>> = [];
  const decisionBodies: Array<Record<string, unknown>> = [];
  server.use(
    http.get(`${API_URL}/inbox`, () =>
      HttpResponse.json({
        items: [
          {
            id: INBOX_ITEM_ID,
            createdAt: 1_700_000_000,
            receivedAt: 1_700_000_000,
            source: 'manual',
            kind: 'task.updated',
            workspaceId: WORKSPACE_ID,
            taskId: 'task-1',
            taskTitle: 'Example task',
          },
        ],
      }),
    ),
    http.post(
      `${API_URL}/workspaces/${WORKSPACE_ID}/ai/suggestions/${INBOX_ITEM_ID}/execute`,
      async ({ request }) => {
        operations.push('execute');
        const body = (await request.json()) as Record<string, unknown>;
        executeBodies.push(body);
        if (executeStatus >= 400) {
          return HttpResponse.json(
            { detail: 'Suggestion execution failed' },
            { status: executeStatus },
          );
        }
        return HttpResponse.json({
          operationId: 'operation-1',
          proposalId: PROPOSAL_ID,
          revision: body.expectedRevision,
          action: body.action,
          status: 'executed',
          inboxItemId: INBOX_ITEM_ID,
          executedAt: 1_700_000_001,
          replayed: false,
        });
      },
    ),
    http.patch(
      `${API_URL}/workspaces/${WORKSPACE_ID}/ai/suggestions/${INBOX_ITEM_ID}/decision`,
      async ({ request }) => {
        operations.push('decision');
        const body = (await request.json()) as Record<string, unknown>;
        decisionBodies.push(body);
        if (decisionStatus >= 400) {
          return HttpResponse.json(
            { detail: 'Suggestion decision failed' },
            { status: decisionStatus },
          );
        }
        return HttpResponse.json({
          proposalId: PROPOSAL_ID,
          revision: 1,
          state: body.state,
          action: body.action,
          reasoning: body.reasoning,
          decisionExpiresAt: 1_800_000_000,
        });
      },
    ),
  );
  return { operations, executeBodies, decisionBodies };
}

function renderInbox(): void {
  renderWithProviders(
    <Suspense fallback={null}>
      <InboxList />
    </Suspense>,
  );
}

async function waitForActionToSettle(): Promise<void> {
  await waitFor(() => {
    const applyButton = screen.queryByRole('button', { name: 'action.apply' });
    expect(applyButton === null || !(applyButton as HTMLButtonElement).disabled).toBe(true);
  });
}

beforeEach(() => {
  seedSession();
  suggestionsStore.getState().clear();
  suggestionsStore.getState().pushSuggestions([SUGGESTION]);
});

afterEach(() => {
  suggestionsStore.getState().clear();
  authStore.getState().clearSession();
  vi.restoreAllMocks();
});

describe('Inbox suggestion actions', () => {
  it('keeps a failed archive command pending without recording it as applied', async () => {
    const user = userEvent.setup();
    const toast = vi.spyOn(toaster, 'show');
    const { operations, executeBodies } = installHandlers(500);
    const archiveSuggestion: Suggestion = { ...SUGGESTION, recommendedAction: 'archive' };
    suggestionsStore.getState().clear();
    suggestionsStore.getState().pushSuggestions([archiveSuggestion]);
    renderInbox();

    await user.click(await screen.findByRole('button', { name: 'action.apply' }));

    await waitFor(() => expect(operations).toContain('execute'));
    expect(executeBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 0,
      action: 'archive',
    });
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' })),
    );
    await waitForActionToSettle();
    expect(suggestionsStore.getState().suggestions).toContainEqual(archiveSuggestion);
  });

  it('saves an edit first and executes the edited action at the new revision', async () => {
    const user = userEvent.setup();
    const { operations, executeBodies, decisionBodies } = installHandlers();
    renderInbox();

    await user.click(await screen.findByRole('button', { name: 'action.edit' }));
    expect(await screen.findByRole('dialog')).toBeTruthy();
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe(SUGGESTION.reasoning);
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
    expect(suggestionsStore.getState().suggestions).toHaveLength(0);
  });

  it('keeps a failed dismissal pending in the inline list', async () => {
    const user = userEvent.setup();
    const toast = vi.spyOn(toaster, 'show');
    const { operations, decisionBodies } = installHandlers(200, 500);
    renderInbox();

    await user.click(await screen.findByRole('button', { name: 'action.dismiss' }));

    await waitFor(() => expect(operations).toEqual(['decision']));
    expect(decisionBodies[0]).toMatchObject({
      proposalId: PROPOSAL_ID,
      expectedRevision: 0,
      state: 'dismissed',
    });
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' })),
    );
    expect(suggestionsStore.getState().suggestions).toContainEqual(SUGGESTION);
  });
});
