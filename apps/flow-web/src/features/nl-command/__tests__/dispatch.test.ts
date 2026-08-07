/**
 * The command palette resolves a natural-language command through a
 * metered LLM call and then has to actually perform it. This covers the
 * half that runs after the resolver returns: which REST call each tool
 * becomes, and — the part that used to be missing — that a tool with no
 * handler produces a refusal rather than a destination.
 *
 * Only the SDK's HTTP layer is stubbed; the dispatch decisions run for
 * real.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
}));

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get, POST: sdkMocks.post, PATCH: sdkMocks.patch },
}));

import type { ResolveCommandResult } from '../api';
import { dispatchToolCall, isDispatchableTool, looksLikePublicId } from '../dispatch';

const WS = 'ws-1';
const TASK_ID = '0195b1a0-1111-7000-8000-000000000001';
const PROJECT_ID = '0195b1a0-2222-7000-8000-000000000002';

function call(tool: string, args: Record<string, unknown>): ResolveCommandResult {
  return { tool, args, confidence: 0.92 } as ResolveCommandResult;
}

function tasksPage(tasks: Array<{ id: string; title: string }>): unknown {
  return { data: { tasks, total: tasks.length }, error: null };
}

function projectsPage(projects: Array<{ id: string; name: string }>): unknown {
  return { data: { projects }, error: null };
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.patch.mockReset();
});

describe('unwired tools', () => {
  // The regression this file exists for: the palette used to fall back to
  // `{ href: '/' }` for every tool it did not special-case, so a resolved
  // command took the user home and looked like it had run.
  it.each(['propose_lens', 'smart_create_task', 'get_task', 'nonexistent_tool'])(
    'refuses %s instead of producing a destination',
    async (tool) => {
      const outcome = await dispatchToolCall(call(tool, { taskId: TASK_ID }), { workspaceId: WS });
      expect(outcome).toEqual({ kind: 'unsupported', tool });
      expect(outcome).not.toHaveProperty('navigateTo');
      expect(sdkMocks.post).not.toHaveBeenCalled();
      expect(sdkMocks.patch).not.toHaveBeenCalled();
    },
  );

  it('reports unwired tools before the user commits to running them', () => {
    expect(isDispatchableTool('propose_lens')).toBe(false);
    expect(isDispatchableTool('smart_create_task')).toBe(false);
    expect(isDispatchableTool('transition_task')).toBe(true);
  });
});

describe('transition_task', () => {
  it('posts the transition for a task named by title', async () => {
    sdkMocks.get.mockResolvedValueOnce(tasksPage([{ id: TASK_ID, title: 'Login bug' }]));
    sdkMocks.post.mockResolvedValueOnce({ data: { ok: true }, error: null });

    const outcome = await dispatchToolCall(
      call('transition_task', { taskId: 'Login bug', transition: 'complete' }),
      { workspaceId: WS },
    );

    expect(sdkMocks.get).toHaveBeenCalledWith('/tasks', {
      params: { query: { workspaceId: WS, q: 'Login bug', limit: 10, offset: 0 } },
    });
    expect(sdkMocks.post).toHaveBeenCalledWith('/tasks/{id}/transitions', {
      params: { path: { id: TASK_ID } },
      body: { transition: 'complete' },
    });
    expect(outcome).toEqual({
      kind: 'executed',
      tool: 'transition_task',
      navigateTo: { href: `/tasks/${TASK_ID}` },
    });
  });

  it('uses a public id verbatim without a lookup', async () => {
    sdkMocks.post.mockResolvedValueOnce({ data: { ok: true }, error: null });
    await dispatchToolCall(call('transition_task', { taskId: TASK_ID, transition: 'start' }), {
      workspaceId: WS,
    });
    expect(sdkMocks.get).not.toHaveBeenCalled();
    expect(sdkMocks.post.mock.calls[0]?.[1]?.params?.path?.id).toBe(TASK_ID);
  });

  it('stops when the title matches several tasks', async () => {
    sdkMocks.get.mockResolvedValueOnce(
      tasksPage([
        { id: TASK_ID, title: 'Login bug on iOS' },
        { id: 'other', title: 'Login bug on Android' },
      ]),
    );
    const outcome = await dispatchToolCall(
      call('transition_task', { taskId: 'Login bug', transition: 'complete' }),
      { workspaceId: WS },
    );
    expect(outcome).toEqual({
      kind: 'unresolved',
      tool: 'transition_task',
      reason: 'ambiguous',
      argument: 'taskId',
      term: 'Login bug',
    });
    expect(sdkMocks.post).not.toHaveBeenCalled();
  });

  it('takes the exact title match when the search also returned near misses', async () => {
    sdkMocks.get.mockResolvedValueOnce(
      tasksPage([
        { id: 'near', title: 'Login bug follow-up' },
        { id: TASK_ID, title: 'Login bug' },
      ]),
    );
    sdkMocks.post.mockResolvedValueOnce({ data: { ok: true }, error: null });
    await dispatchToolCall(call('transition_task', { taskId: 'login bug', transition: 'cancel' }), {
      workspaceId: WS,
    });
    expect(sdkMocks.post.mock.calls[0]?.[1]?.params?.path?.id).toBe(TASK_ID);
  });

  it('stops when nothing matches', async () => {
    sdkMocks.get.mockResolvedValueOnce(tasksPage([]));
    const outcome = await dispatchToolCall(
      call('transition_task', { taskId: 'ghost', transition: 'complete' }),
      { workspaceId: WS },
    );
    expect(outcome).toMatchObject({ kind: 'unresolved', reason: 'not_found', term: 'ghost' });
    expect(sdkMocks.post).not.toHaveBeenCalled();
  });

  it('rejects a transition verb the API does not accept', async () => {
    const outcome = await dispatchToolCall(
      call('transition_task', { taskId: TASK_ID, transition: 'done' }),
      { workspaceId: WS },
    );
    expect(outcome).toMatchObject({
      kind: 'unresolved',
      argument: 'transition',
      reason: 'not_found',
    });
    expect(sdkMocks.post).not.toHaveBeenCalled();
  });
});

describe('create_task', () => {
  it('creates the task and lands on it', async () => {
    sdkMocks.get.mockResolvedValueOnce(projectsPage([{ id: PROJECT_ID, name: 'Platform' }]));
    sdkMocks.post.mockResolvedValueOnce({ data: { id: TASK_ID }, error: null });

    const outcome = await dispatchToolCall(
      call('create_task', { title: 'Write the changelog', priority: 3 }),
      { workspaceId: WS },
    );

    expect(sdkMocks.post).toHaveBeenCalledWith('/tasks', {
      body: {
        projectId: PROJECT_ID,
        title: 'Write the changelog',
        visibility: 'public',
        priority: 3,
      },
    });
    expect(outcome).toEqual({
      kind: 'executed',
      tool: 'create_task',
      navigateTo: { href: `/tasks/${TASK_ID}` },
    });
  });

  it('resolves the project by name', async () => {
    sdkMocks.get.mockResolvedValueOnce(
      projectsPage([
        { id: 'p-other', name: 'Marketing' },
        { id: PROJECT_ID, name: 'Platform' },
      ]),
    );
    sdkMocks.post.mockResolvedValueOnce({ data: { id: TASK_ID }, error: null });
    await dispatchToolCall(call('create_task', { title: 'Ship it', projectId: 'Platform' }), {
      workspaceId: WS,
    });
    expect(sdkMocks.post.mock.calls[0]?.[1]?.body?.projectId).toBe(PROJECT_ID);
  });

  it('does not guess a project when the workspace has several', async () => {
    sdkMocks.get.mockResolvedValueOnce(
      projectsPage([
        { id: 'p-1', name: 'Marketing' },
        { id: 'p-2', name: 'Platform' },
      ]),
    );
    const outcome = await dispatchToolCall(call('create_task', { title: 'Ship it' }), {
      workspaceId: WS,
    });
    expect(outcome).toMatchObject({ kind: 'unresolved', argument: 'projectId', reason: 'missing' });
    expect(sdkMocks.post).not.toHaveBeenCalled();
  });

  it('reports a server refusal instead of navigating', async () => {
    sdkMocks.get.mockResolvedValueOnce(projectsPage([{ id: PROJECT_ID, name: 'Platform' }]));
    sdkMocks.post.mockResolvedValueOnce({ data: undefined, error: { title: 'nope' } });
    const outcome = await dispatchToolCall(call('create_task', { title: 'Ship it' }), {
      workspaceId: WS,
    });
    expect(outcome).toEqual({ kind: 'failed', tool: 'create_task' });
    expect(outcome).not.toHaveProperty('navigateTo');
  });
});

describe('update_task', () => {
  it('patches only the fields the command carried', async () => {
    sdkMocks.patch.mockResolvedValueOnce({ data: { id: TASK_ID }, error: null });
    await dispatchToolCall(
      call('update_task', { taskId: TASK_ID, title: 'New title', dueOn: '2026-08-31' }),
      { workspaceId: WS },
    );
    expect(sdkMocks.patch).toHaveBeenCalledWith('/tasks/{id}', {
      params: { path: { id: TASK_ID } },
      body: { title: 'New title', dueOn: '2026-08-31' },
    });
  });

  it('does not send an empty patch', async () => {
    const outcome = await dispatchToolCall(call('update_task', { taskId: TASK_ID }), {
      workspaceId: WS,
    });
    expect(outcome).toMatchObject({ kind: 'unresolved', reason: 'missing' });
    expect(sdkMocks.patch).not.toHaveBeenCalled();
  });
});

describe('add_comment', () => {
  it('posts the comment on the resolved task', async () => {
    sdkMocks.post.mockResolvedValueOnce({ data: { id: 'c-1' }, error: null });
    const outcome = await dispatchToolCall(
      call('add_comment', { taskId: TASK_ID, body: 'Looks good' }),
      { workspaceId: WS },
    );
    expect(sdkMocks.post).toHaveBeenCalledWith('/tasks/{id}/comments', {
      params: { path: { id: TASK_ID } },
      body: { body: 'Looks good' },
    });
    expect(outcome).toMatchObject({ kind: 'executed' });
  });

  it('will not post an empty comment', async () => {
    const outcome = await dispatchToolCall(call('add_comment', { taskId: TASK_ID, body: '  ' }), {
      workspaceId: WS,
    });
    expect(outcome).toMatchObject({ kind: 'unresolved', argument: 'body' });
    expect(sdkMocks.post).not.toHaveBeenCalled();
  });
});

describe('read tools', () => {
  it('hands search_tasks back to the palette search', async () => {
    const outcome = await dispatchToolCall(call('search_tasks', { query: 'invoice' }), {
      workspaceId: WS,
    });
    expect(outcome).toEqual({ kind: 'search', tool: 'search_tasks', query: 'invoice' });
  });

  it('sends list_projects to the workspace project list', async () => {
    const outcome = await dispatchToolCall(call('list_projects', {}), { workspaceId: WS });
    expect(outcome).toEqual({
      kind: 'navigated',
      tool: 'list_projects',
      navigateTo: { href: `/workspaces/${WS}/projects` },
    });
  });

  it('sends list_tasks to the named project', async () => {
    sdkMocks.get.mockResolvedValueOnce(projectsPage([{ id: PROJECT_ID, name: 'Platform' }]));
    const outcome = await dispatchToolCall(call('list_tasks', { projectId: 'Platform' }), {
      workspaceId: WS,
    });
    expect(outcome).toMatchObject({
      kind: 'navigated',
      navigateTo: { href: `/workspaces/${WS}/projects/${PROJECT_ID}/tasks` },
    });
  });
});

describe('looksLikePublicId', () => {
  it('accepts a canonical UUID and rejects prose', () => {
    expect(looksLikePublicId(TASK_ID)).toBe(true);
    expect(looksLikePublicId(TASK_ID.toUpperCase())).toBe(true);
    expect(looksLikePublicId('Login bug')).toBe(false);
    expect(looksLikePublicId('0195b1a0-1111-7000-8000-00000000000')).toBe(false);
    expect(looksLikePublicId('0195b1a0_1111_7000_8000_000000000001')).toBe(false);
    expect(looksLikePublicId('0195b1a0-1111-7000-8000-00000000000z')).toBe(false);
  });
});
