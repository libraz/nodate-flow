import { toaster } from '@nodate-flow/ui/primitives/toast';
import { act, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { HttpResponse, http } from 'msw';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { API_URL, server, useMockApi } from '../../../../tests/msw/server';
import { authStore } from '../../auth/auth-store';
import { type AiSuggestion, aiSuggestionsKeys } from '../api';
import { type Suggestion, suggestionsStore } from '../store';
import {
  type SuggestionActionExecutor,
  useSuggestionActionExecutor,
} from '../use-suggestion-action-executor';

useMockApi();

const WORKSPACE_ID = 'ws-1';
const ACTOR_ID = 'actor-1';
const INBOX_ITEM_ID = 'inbox-1';

const DTO: AiSuggestion = {
  eventId: 'proposal-1',
  proposalId: 'proposal-1',
  inboxItemId: INBOX_ITEM_ID,
  revision: 0,
  sourceVersionStatus: 'captured',
  proposedAt: 1_700_000_000,
  recommendedAction: 'archive',
  reasoning: 'Archive this item.',
  score: 0.9,
};

const SUGGESTION: Suggestion = { ...DTO, workspaceId: WORKSPACE_ID, actorId: ACTOR_ID };

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

function conflict(code: string): Response {
  return HttpResponse.json({ type: code, detail: code, status: 409 }, { status: 409 });
}

const EXECUTE_URL = `${API_URL}/workspaces/${WORKSPACE_ID}/ai/suggestions/${INBOX_ITEM_ID}/execute`;
const DECISION_URL = `${API_URL}/workspaces/${WORKSPACE_ID}/ai/suggestions/${INBOX_ITEM_ID}/decision`;
const LIST_URL = `${API_URL}/workspaces/${WORKSPACE_ID}/ai/suggestions`;

function renderExecutor() {
  const ref: { current: SuggestionActionExecutor | null } = { current: null };
  function Harness(): null {
    ref.current = useSuggestionActionExecutor(WORKSPACE_ID, () => undefined);
    return null;
  }
  const rendered = renderWithProviders(<Harness />);
  rendered.queryClient.setQueryData(aiSuggestionsKeys.list(WORKSPACE_ID, ACTOR_ID), [DTO]);
  const invalidate = vi.spyOn(rendered.queryClient, 'invalidateQueries');
  return {
    executor: (): SuggestionActionExecutor => {
      if (!ref.current) throw new Error('executor not mounted');
      return ref.current;
    },
    queryClient: rendered.queryClient,
    invalidate,
  };
}

function invalidatedListPrefix(invalidate: { mock: { calls: unknown[][] } }): boolean {
  return invalidate.mock.calls.some(
    ([filters]) =>
      JSON.stringify((filters as { queryKey?: unknown } | undefined)?.queryKey) ===
      JSON.stringify(aiSuggestionsKeys.listPrefix(WORKSPACE_ID)),
  );
}

beforeEach(() => {
  seedSession();
  vi.spyOn(toaster, 'show').mockImplementation(() => '');
  suggestionsStore.getState().clear();
  suggestionsStore.getState().pushSuggestions([SUGGESTION]);
});

afterEach(() => {
  suggestionsStore.getState().clear();
  authStore.getState().clearSession();
  vi.restoreAllMocks();
});

describe('useSuggestionActionExecutor conflicts', () => {
  it('drops an already-resolved suggestion from the store and the list cache', async () => {
    server.use(http.post(EXECUTE_URL, () => conflict('AI.SUGGESTION.ALREADY_RESOLVED')));
    const { executor, queryClient, invalidate } = renderExecutor();

    let outcome: unknown;
    await act(async () => {
      outcome = await executor().execute(SUGGESTION);
    });

    expect(outcome).toEqual({ status: 'failed' });
    expect(suggestionsStore.getState().suggestions).toHaveLength(0);
    expect(queryClient.getQueryData(aiSuggestionsKeys.list(WORKSPACE_ID, ACTOR_ID))).toEqual([]);
    expect(invalidatedListPrefix(invalidate)).toBe(true);
  });

  it('drops an already-resolved suggestion when a dismissal conflicts', async () => {
    server.use(http.patch(DECISION_URL, () => conflict('AI.SUGGESTION.ALREADY_RESOLVED')));
    const { executor, queryClient, invalidate } = renderExecutor();

    await act(async () => {
      await executor().dismiss(SUGGESTION);
    });

    expect(suggestionsStore.getState().suggestions).toHaveLength(0);
    expect(queryClient.getQueryData(aiSuggestionsKeys.list(WORKSPACE_ID, ACTOR_ID))).toEqual([]);
    expect(invalidatedListPrefix(invalidate)).toBe(true);
  });

  it('refreshes the list and the inline entry when the evidence changed', async () => {
    const fresh: AiSuggestion = { ...DTO, revision: 3, reasoning: 'Re-evaluated reasoning.' };
    server.use(
      http.post(EXECUTE_URL, () => conflict('AI.SUGGESTION.EVIDENCE_CHANGED')),
      http.get(LIST_URL, () => HttpResponse.json({ suggestions: [fresh] })),
    );
    const { executor, invalidate } = renderExecutor();

    await act(async () => {
      await executor().execute(SUGGESTION);
    });

    expect(invalidatedListPrefix(invalidate)).toBe(true);
    await waitFor(() =>
      expect(suggestionsStore.getState().suggestions).toEqual([
        { ...fresh, workspaceId: WORKSPACE_ID, actorId: ACTOR_ID },
      ]),
    );
  });

  it('removes the inline entry when the refreshed list no longer has it', async () => {
    server.use(
      http.patch(DECISION_URL, () => conflict('AI.SUGGESTION.EVIDENCE_CHANGED')),
      http.get(LIST_URL, () => HttpResponse.json({ suggestions: [] })),
    );
    const { executor } = renderExecutor();

    await act(async () => {
      await executor().saveEdit(SUGGESTION, { recommendedAction: 'snooze', reasoning: 'Later.' });
    });

    await waitFor(() => expect(suggestionsStore.getState().suggestions).toHaveLength(0));
  });
});

describe('useSuggestionActionExecutor double submit', () => {
  function decisionHandler(requests: string[]) {
    return http.patch(DECISION_URL, async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>;
      requests.push(String(body.state));
      return HttpResponse.json({
        proposalId: DTO.proposalId,
        revision: 1,
        state: body.state,
        action: body.action,
        reasoning: body.reasoning,
        decisionExpiresAt: 1_800_000_000,
      });
    });
  }

  it('sends one dismissal for a double click', async () => {
    const requests: string[] = [];
    server.use(decisionHandler(requests));
    const { executor } = renderExecutor();

    let results: boolean[] = [];
    await act(async () => {
      const current = executor();
      results = await Promise.all([current.dismiss(SUGGESTION), current.dismiss(SUGGESTION)]);
    });

    expect(requests).toEqual(['dismissed']);
    expect(results).toEqual([true, false]);
  });

  it('sends one edit for a double click', async () => {
    const requests: string[] = [];
    server.use(decisionHandler(requests));
    const { executor } = renderExecutor();
    const patch = { recommendedAction: 'snooze' as const, reasoning: 'Later.' };

    let results: boolean[] = [];
    await act(async () => {
      const current = executor();
      results = await Promise.all([
        current.saveEdit(SUGGESTION, patch),
        current.saveEdit(SUGGESTION, patch),
      ]);
    });

    expect(requests).toEqual(['edited']);
    expect(results).toEqual([true, false]);
  });
});
