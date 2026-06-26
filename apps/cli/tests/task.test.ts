/**
 * Unit tests for `tnk task` subcommand builders and executors.
 *
 * The CLI parser (`@libraz/node-cli`) is bypassed: tests call the
 * pure helpers in `src/task-builders.ts` directly with a stubbed SDK
 * client and assert that the right method was invoked with the right
 * URL, params, and body shape.
 */

import { describe, expect, it, vi } from 'vitest';

import {
  buildSearchQuery,
  buildUpdatePlan,
  executeSearch,
  executeUpdate,
  isStateTransition,
  type SdkClientLike,
  STATE_TRANSITIONS,
} from '../src/task-builders.js';

/* ── Test fixtures ────────────────────────────────────────────── */

/**
 * Build an SDK client mock whose GET / POST / PATCH all resolve with
 * the given response. Each method is a `vi.fn()` so tests can assert
 * call arguments.
 */
function createSdkMock(
  response: { data?: unknown; error?: unknown } = { data: { ok: true } },
): SdkClientLike {
  return {
    // biome-ignore lint/style/useNamingConvention: SDK method name
    GET: vi.fn().mockResolvedValue(response),
    // biome-ignore lint/style/useNamingConvention: SDK method name
    POST: vi.fn().mockResolvedValue(response),
    // biome-ignore lint/style/useNamingConvention: SDK method name
    PATCH: vi.fn().mockResolvedValue(response),
  };
}

/* ── buildUpdatePlan ──────────────────────────────────────────── */

describe('buildUpdatePlan', () => {
  it('returns undefined when no flags are set', () => {
    expect(buildUpdatePlan({})).toBeUndefined();
  });

  it('maps the title flag to the patch body', () => {
    const plan = buildUpdatePlan({ title: 'New title' });
    expect(plan).toEqual({ patchBody: { title: 'New title' } });
  });

  it('renames `due` to `dueOn` and `start` to `startOn` for the API', () => {
    const plan = buildUpdatePlan({ due: '2099-12-31', start: '2099-01-01' });
    expect(plan).toEqual({
      patchBody: { dueOn: '2099-12-31', startOn: '2099-01-01' },
    });
  });

  it('forwards priority and visibility verbatim', () => {
    const plan = buildUpdatePlan({ priority: 3, visibility: 'project' });
    expect(plan).toEqual({
      patchBody: { priority: 3, visibility: 'project' },
    });
  });

  it('captures a known state transition separately from the patch body', () => {
    const plan = buildUpdatePlan({ state: 'complete' });
    expect(plan).toEqual({ stateTransition: 'complete' });
  });

  it('combines patch fields and a state transition into the same plan', () => {
    const plan = buildUpdatePlan({ title: 'Hello', state: 'start' });
    expect(plan).toEqual({
      patchBody: { title: 'Hello' },
      stateTransition: 'start',
    });
  });

  it('drops unknown state values rather than forwarding them as transitions', () => {
    // Passing an unrecognised state with no other flags is treated as
    // "no fields to update" because nothing actionable remains.
    expect(buildUpdatePlan({ state: 'totally-bogus' })).toBeUndefined();
  });
});

/* ── isStateTransition ────────────────────────────────────────── */

describe('isStateTransition', () => {
  it('accepts every value in STATE_TRANSITIONS', () => {
    for (const t of STATE_TRANSITIONS) {
      expect(isStateTransition(t)).toBe(true);
    }
  });

  it('rejects unknown strings', () => {
    expect(isStateTransition('done')).toBe(false);
    expect(isStateTransition('')).toBe(false);
    expect(isStateTransition(undefined)).toBe(false);
    expect(isStateTransition(42)).toBe(false);
  });
});

/* ── executeUpdate ────────────────────────────────────────────── */

describe('executeUpdate', () => {
  it('issues PATCH /tasks/{id} with the planned body', async () => {
    const sdk = createSdkMock({ data: { id: 'tsk-1', title: 'X', derivedState: 'open' } });
    const result = await executeUpdate(sdk, 'tsk-1', {
      patchBody: { title: 'X', priority: 2 },
    });

    expect(sdk.PATCH).toHaveBeenCalledTimes(1);
    expect(sdk.PATCH).toHaveBeenCalledWith('/tasks/{id}', {
      params: { path: { id: 'tsk-1' } },
      body: { title: 'X', priority: 2 },
    });
    expect(sdk.POST).not.toHaveBeenCalled();
    expect(result.error).toBeUndefined();
  });

  it('issues POST /tasks/{id}/transitions with the transition body', async () => {
    const sdk = createSdkMock({ data: { id: 'tsk-1', title: 'X', derivedState: 'waiting' } });
    await executeUpdate(sdk, 'tsk-1', { stateTransition: 'start' });

    expect(sdk.POST).toHaveBeenCalledTimes(1);
    expect(sdk.POST).toHaveBeenCalledWith('/tasks/{id}/transitions', {
      params: { path: { id: 'tsk-1' } },
      body: { transition: 'start' },
    });
    expect(sdk.PATCH).not.toHaveBeenCalled();
  });

  it('runs PATCH first and POST second when both are requested', async () => {
    const sdk = createSdkMock({ data: { id: 'tsk-1', title: 'Y', derivedState: 'done' } });
    await executeUpdate(sdk, 'tsk-1', {
      patchBody: { title: 'Y' },
      stateTransition: 'complete',
    });

    expect(sdk.PATCH).toHaveBeenCalledTimes(1);
    expect(sdk.POST).toHaveBeenCalledTimes(1);
    const patchOrder = (sdk.PATCH as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0];
    const postOrder = (sdk.POST as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0];
    expect(patchOrder).toBeLessThan(postOrder);
  });

  it('short-circuits and forwards the error from a failing PATCH', async () => {
    const sdk: SdkClientLike = {
      // biome-ignore lint/style/useNamingConvention: SDK method name
      GET: vi.fn(),
      // biome-ignore lint/style/useNamingConvention: SDK method name
      POST: vi.fn(),
      // biome-ignore lint/style/useNamingConvention: SDK method name
      PATCH: vi.fn().mockResolvedValue({ error: { detail: 'nope' } }),
    };
    const result = await executeUpdate(sdk, 'tsk-1', {
      patchBody: { title: 'X' },
      stateTransition: 'start',
    });
    expect(result.error).toEqual({ detail: 'nope' });
    expect(sdk.POST).not.toHaveBeenCalled();
  });
});

/* ── buildSearchQuery ─────────────────────────────────────────── */

describe('buildSearchQuery', () => {
  it('rejects an empty query', () => {
    expect(buildSearchQuery('   ', { workspaceId: 'ws-1' })).toBe('empty_query');
  });

  it('rejects when neither workspace nor project is given', () => {
    expect(buildSearchQuery('hello', {})).toBe('missing_scope');
  });

  it('builds a workspace-scoped query', () => {
    const q = buildSearchQuery('  hello  ', { workspaceId: 'ws-1' });
    expect(q).toEqual({ q: 'hello', limit: 20, workspaceId: 'ws-1' });
  });

  it('builds a project-scoped query and respects --limit', () => {
    const q = buildSearchQuery('hello', { projectId: 'proj-1', limit: 5 });
    expect(q).toEqual({ q: 'hello', limit: 5, projectId: 'proj-1' });
  });
});

/* ── executeSearch ────────────────────────────────────────────── */

describe('executeSearch', () => {
  it('issues GET /tasks with the query parameters', async () => {
    const sdk = createSdkMock({ data: { tasks: [], total: 0 } });
    await executeSearch(sdk, { q: 'hello', limit: 20, workspaceId: 'ws-1' });

    expect(sdk.GET).toHaveBeenCalledTimes(1);
    expect(sdk.GET).toHaveBeenCalledWith('/tasks', {
      params: { query: { q: 'hello', limit: 20, workspaceId: 'ws-1' } },
    });
  });

  it('forwards SDK errors back to the caller', async () => {
    const sdk: SdkClientLike = {
      // biome-ignore lint/style/useNamingConvention: SDK method name
      GET: vi.fn().mockResolvedValue({ error: { detail: 'boom' } }),
      // biome-ignore lint/style/useNamingConvention: SDK method name
      POST: vi.fn(),
      // biome-ignore lint/style/useNamingConvention: SDK method name
      PATCH: vi.fn(),
    };
    const result = await executeSearch(sdk, { q: 'x', limit: 1, projectId: 'p' });
    expect(result.error).toEqual({ detail: 'boom' });
  });
});
