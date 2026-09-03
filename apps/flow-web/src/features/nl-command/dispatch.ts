/**
 * NL command dispatch — turns a resolved MCP tool call into a real effect.
 *
 * The resolver (POST /workspaces/{wsId}/ai/resolve-command) costs a
 * metered LLM call and returns a tool name plus arguments. Everything
 * downstream of that is this module's job: map the tool onto the REST
 * call that performs it, or say plainly that the palette cannot perform
 * it. There is deliberately no fallback destination — a tool with no
 * handler resolves to `unsupported`, never to a navigation, because a
 * navigation is indistinguishable from success to the person watching.
 *
 * This is a dispatch table, not a tool catalogue. Tool names and argument
 * shapes come from the server (the MCP registry feeds the resolver's
 * prompt); what lives here is the one thing the server cannot know —
 * which REST endpoint of this web client performs each tool, and which
 * tools have no surface in it at all.
 *
 * Public ids: the model is never shown any, so it is told to put the
 * user's own wording in an id field when it has nothing better. A value
 * that is not a public id is therefore treated as a title to look up,
 * and an ambiguous or missing match stops the dispatch instead of
 * guessing.
 */

import { apiRequest } from '../../lib/api';
import type { TransitionName } from '../tasks/api';
import type { ResolveCommandResult } from './api';

/**
 * Stand-in the requester hands back when a dispatch call fails. A
 * dispatch reports the refusal as its own outcome rather than letting
 * it reach the user as an error, so it needs a value that cannot be
 * confused with a bodyless success.
 */
const FAILED = Symbol('dispatch failed');

/** Where the palette should send the user once a dispatch is done. */
export interface NavTarget {
  href: string;
  search?: Record<string, unknown>;
}

/** Why a dispatch could not build a call from the resolved arguments. */
export type UnresolvedReason = 'missing' | 'not_found' | 'ambiguous';

export type DispatchOutcome =
  /** A write went through; `navigateTo` shows the user what changed. */
  | { kind: 'executed'; tool: string; navigateTo: NavTarget }
  /** A read tool resolved to a destination. Nothing was mutated. */
  | { kind: 'navigated'; tool: string; navigateTo: NavTarget }
  /** A read tool the palette answers itself: rerun as a palette search. */
  | { kind: 'search'; tool: string; query: string }
  /** The tool has no surface in this client. Never navigates. */
  | { kind: 'unsupported'; tool: string }
  /** The arguments do not name something we can act on. Never navigates. */
  | {
      kind: 'unresolved';
      tool: string;
      reason: UnresolvedReason;
      argument: string;
      term: string;
    }
  /** The call was made and the server refused it. */
  | { kind: 'failed'; tool: string };

export interface DispatchContext {
  /** Public id of the workspace the command was resolved against. */
  workspaceId: string;
}

type ToolArgs = Record<string, unknown>;

type ToolHandler = (args: ToolArgs, ctx: DispatchContext) => Promise<DispatchOutcome>;

/* ── Argument helpers ─────────────────────────────────────────── */

/** Reads a non-empty trimmed string argument, or undefined. */
function text(args: ToolArgs, key: string): string | undefined {
  const raw = args[key];
  if (typeof raw !== 'string') return undefined;
  const trimmed = raw.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

/** Reads an integer argument within an inclusive range, or undefined. */
function integer(args: ToolArgs, key: string, min: number, max: number): number | undefined {
  const raw = args[key];
  const n = typeof raw === 'number' ? raw : typeof raw === 'string' ? Number(raw) : Number.NaN;
  if (!Number.isInteger(n) || n < min || n > max) return undefined;
  return n;
}

const HEX = '0123456789abcdefABCDEF';

/**
 * True when the value has the shape of a public id (UUID v7 in canonical
 * 8-4-4-4-12 hex form). Checked character by character rather than with a
 * pattern: matching in resolution paths avoids regex project-wide
 * (docs/conventions/autonomy.md).
 */
export function looksLikePublicId(value: string): boolean {
  if (value.length !== 36) return false;
  const groups = [8, 4, 4, 4, 12];
  let at = 0;
  for (let g = 0; g < groups.length; g += 1) {
    if (g > 0) {
      if (value[at] !== '-') return false;
      at += 1;
    }
    const len = groups[g] ?? 0;
    for (let i = 0; i < len; i += 1) {
      const ch = value[at];
      if (ch === undefined || !HEX.includes(ch)) return false;
      at += 1;
    }
  }
  return true;
}

/**
 * The transition verbs the API accepts. Declared as a total record over
 * the SDK's `TransitionName` union so a verb added to the OpenAPI enum
 * fails this file at typecheck instead of silently never being dispatched.
 */
const TRANSITIONS: Record<TransitionName, true> = {
  start: true,
  block: true,
  unblock: true,
  submit: true,
  complete: true,
  reopen: true,
  cancel: true,
};

function asTransition(value: string): TransitionName | undefined {
  return Object.hasOwn(TRANSITIONS, value) ? (value as TransitionName) : undefined;
}

/* ── Target lookup ────────────────────────────────────────────── */

type Lookup = { id: string } | { reason: UnresolvedReason };

/**
 * Picks the single row whose title matches `term` exactly (ignoring case)
 * when several rows came back, so "login bug" still resolves when the
 * search also returned "login bug follow-up".
 */
function pickUnique(rows: Array<{ id: string; title: string }>, term: string): Lookup {
  if (rows.length === 0) return { reason: 'not_found' };
  const only = rows[0];
  if (rows.length === 1 && only) return { id: only.id };
  const needle = term.toLowerCase();
  const exact = rows.filter((r) => r.title.trim().toLowerCase() === needle);
  const first = exact[0];
  if (exact.length === 1 && first) return { id: first.id };
  return { reason: 'ambiguous' };
}

/**
 * Resolves the `taskId` argument. A public id is used as-is; anything
 * else is searched for by title within the workspace.
 */
async function resolveTask(term: string | undefined, ctx: DispatchContext): Promise<Lookup> {
  if (term === undefined) return { reason: 'missing' };
  if (looksLikePublicId(term)) return { id: term };
  // A lookup that cannot reach the server is reported as "no match",
  // which the caller already renders as an unresolved argument rather
  // than as a command that ran.
  const data = await apiRequest(
    (client) =>
      client.GET('/tasks', {
        params: { query: { workspaceId: ctx.workspaceId, q: term, limit: 10, offset: 0 } },
      }),
    'Failed to look up tasks',
    { onError: 'empty', empty: null },
  );
  if (!data) return { reason: 'not_found' };
  return pickUnique(
    (data.tasks ?? []).map((task) => ({ id: task.id, title: task.title })),
    term,
  );
}

/**
 * Resolves the `projectId` argument. A public id is used as-is, a name is
 * matched against the workspace's projects, and an absent argument falls
 * back to the sole project when the workspace has exactly one — the
 * common single-project case, where asking would be noise.
 */
async function resolveProject(term: string | undefined, ctx: DispatchContext): Promise<Lookup> {
  if (term !== undefined && looksLikePublicId(term)) return { id: term };
  const data = await apiRequest(
    (client) =>
      client.GET('/workspaces/{wsId}/projects', { params: { path: { wsId: ctx.workspaceId } } }),
    'Failed to look up projects',
    { onError: 'empty', empty: null },
  );
  if (!data) return { reason: 'not_found' };
  const projects = (data.projects ?? []).map((p) => ({ id: p.id, title: p.name }));
  if (term === undefined) {
    const sole = projects[0];
    if (projects.length === 1 && sole) return { id: sole.id };
    return { reason: 'missing' };
  }
  return pickUnique(projects, term);
}

function unresolved(
  tool: string,
  argument: string,
  lookup: { reason: UnresolvedReason },
  term: string,
): DispatchOutcome {
  return { kind: 'unresolved', tool, reason: lookup.reason, argument, term };
}

/* ── Tool handlers ────────────────────────────────────────────── */

/**
 * create_task runs the REST create rather than deep-linking into the
 * create dialog. The palette promises execution ("press Enter to
 * execute"), and a prefilled form is not that; it also drops every
 * argument the route's search schema does not carry. The user lands on
 * the created task, which is the evidence that it happened.
 */
const createTask: ToolHandler = async (args, ctx) => {
  const title = text(args, 'title');
  if (title === undefined) {
    return {
      kind: 'unresolved',
      tool: 'create_task',
      reason: 'missing',
      argument: 'title',
      term: '',
    };
  }
  const projectTerm = text(args, 'projectId') ?? text(args, 'project');
  const project = await resolveProject(projectTerm, ctx);
  if (!('id' in project)) return unresolved('create_task', 'projectId', project, projectTerm ?? '');

  const description = text(args, 'description');
  const dueOn = text(args, 'dueOn');
  const startOn = text(args, 'startOn');
  const priority = integer(args, 'priority', 0, 4);
  const data = await apiRequest(
    (client) =>
      client.POST('/tasks', {
        body: {
          projectId: project.id,
          title,
          // Same default the create dialog applies: a task created from a
          // command is a normal project task, not a private one.
          visibility: 'public' as const,
          ...(description !== undefined ? { description } : {}),
          ...(dueOn !== undefined ? { dueOn } : {}),
          ...(startOn !== undefined ? { startOn } : {}),
          ...(priority !== undefined ? { priority } : {}),
        },
      }),
    'Failed to create task',
    { onError: 'empty', empty: null },
  );
  if (!data) return { kind: 'failed', tool: 'create_task' };
  return { kind: 'executed', tool: 'create_task', navigateTo: { href: `/tasks/${data.id}` } };
};

const updateTask: ToolHandler = async (args, ctx) => {
  const term = text(args, 'taskId');
  const task = await resolveTask(term, ctx);
  if (!('id' in task)) return unresolved('update_task', 'taskId', task, term ?? '');

  const title = text(args, 'title');
  const description = text(args, 'description');
  const dueOn = text(args, 'dueOn');
  const startOn = text(args, 'startOn');
  const priority = integer(args, 'priority', 0, 4);
  const patch = {
    ...(title !== undefined ? { title } : {}),
    ...(description !== undefined ? { description } : {}),
    ...(dueOn !== undefined ? { dueOn } : {}),
    ...(startOn !== undefined ? { startOn } : {}),
    ...(priority !== undefined ? { priority } : {}),
  };
  if (Object.keys(patch).length === 0) {
    return {
      kind: 'unresolved',
      tool: 'update_task',
      reason: 'missing',
      argument: 'title',
      term: term ?? '',
    };
  }
  // The stand-in is a sentinel rather than a falsy value: an endpoint
  // that answers 204 has no body, and "no body" must not read as
  // "the call failed".
  const updated = await apiRequest(
    (client) => client.PATCH('/tasks/{id}', { params: { path: { id: task.id } }, body: patch }),
    'Failed to update task',
    { onError: 'empty', empty: FAILED },
  );
  if (updated === FAILED) return { kind: 'failed', tool: 'update_task' };
  return { kind: 'executed', tool: 'update_task', navigateTo: { href: `/tasks/${task.id}` } };
};

const transitionTask: ToolHandler = async (args, ctx) => {
  const raw = text(args, 'transition');
  const transition = raw === undefined ? undefined : asTransition(raw);
  if (transition === undefined) {
    return {
      kind: 'unresolved',
      tool: 'transition_task',
      reason: raw === undefined ? 'missing' : 'not_found',
      argument: 'transition',
      term: raw ?? '',
    };
  }
  const term = text(args, 'taskId');
  const task = await resolveTask(term, ctx);
  if (!('id' in task)) return unresolved('transition_task', 'taskId', task, term ?? '');

  const reason = text(args, 'reason');
  const moved = await apiRequest(
    (client) =>
      client.POST('/tasks/{id}/transitions', {
        params: { path: { id: task.id } },
        body: { transition, ...(reason !== undefined ? { reason } : {}) },
      }),
    'Failed to transition task',
    { onError: 'empty', empty: FAILED },
  );
  if (moved === FAILED) return { kind: 'failed', tool: 'transition_task' };
  return { kind: 'executed', tool: 'transition_task', navigateTo: { href: `/tasks/${task.id}` } };
};

const addComment: ToolHandler = async (args, ctx) => {
  const body = text(args, 'body');
  if (body === undefined) {
    return {
      kind: 'unresolved',
      tool: 'add_comment',
      reason: 'missing',
      argument: 'body',
      term: '',
    };
  }
  const term = text(args, 'taskId');
  const task = await resolveTask(term, ctx);
  if (!('id' in task)) return unresolved('add_comment', 'taskId', task, term ?? '');

  const posted = await apiRequest(
    (client) =>
      client.POST('/tasks/{id}/comments', { params: { path: { id: task.id } }, body: { body } }),
    'Failed to add comment',
    { onError: 'empty', empty: FAILED },
  );
  if (posted === FAILED) return { kind: 'failed', tool: 'add_comment' };
  return { kind: 'executed', tool: 'add_comment', navigateTo: { href: `/tasks/${task.id}` } };
};

/**
 * search_tasks is answered by the palette itself: it already runs the
 * same GET /tasks search and renders the hits inline, so handing the
 * query back is the execution, not a stand-in for it.
 */
const searchTasks: ToolHandler = async (args) => {
  const query = text(args, 'query');
  if (query === undefined) {
    return {
      kind: 'unresolved',
      tool: 'search_tasks',
      reason: 'missing',
      argument: 'query',
      term: '',
    };
  }
  return { kind: 'search', tool: 'search_tasks', query };
};

const listTasks: ToolHandler = async (args, ctx) => {
  const term = text(args, 'projectId') ?? text(args, 'project');
  const project = await resolveProject(term, ctx);
  if (!('id' in project)) {
    // No project named and none to infer: the workspace's project list is
    // where a task list is chosen, so that is the honest destination.
    if (project.reason === 'missing') {
      return {
        kind: 'navigated',
        tool: 'list_tasks',
        navigateTo: { href: `/workspaces/${ctx.workspaceId}/projects` },
      };
    }
    return unresolved('list_tasks', 'projectId', project, term ?? '');
  }
  return {
    kind: 'navigated',
    tool: 'list_tasks',
    navigateTo: { href: `/workspaces/${ctx.workspaceId}/projects/${project.id}/tasks` },
  };
};

const listProjects: ToolHandler = async (_args, ctx) => ({
  kind: 'navigated',
  tool: 'list_projects',
  navigateTo: { href: `/workspaces/${ctx.workspaceId}/projects` },
});

/**
 * Tools the palette can perform. Absence is meaningful: `propose_lens`
 * and `smart_create_task` are resolvable server-side but have no landing
 * surface in this client (a compiled lens is applied from the dock, and
 * smart_create_task's project inference has no REST equivalent), so they
 * are reported as unsupported rather than approximated.
 */
const HANDLERS = new Map<string, ToolHandler>([
  ['create_task', createTask],
  ['update_task', updateTask],
  ['transition_task', transitionTask],
  ['add_comment', addComment],
  ['search_tasks', searchTasks],
  ['list_tasks', listTasks],
  ['list_projects', listProjects],
]);

/** True when the palette has a handler for this tool. */
export function isDispatchableTool(tool: string): boolean {
  return HANDLERS.has(tool);
}

/**
 * Performs a resolved tool call. Never throws: transport failures and
 * unknown tools both come back as an outcome the palette can render.
 */
export async function dispatchToolCall(
  result: ResolveCommandResult,
  ctx: DispatchContext,
): Promise<DispatchOutcome> {
  const handler = HANDLERS.get(result.tool);
  if (!handler) return { kind: 'unsupported', tool: result.tool };
  try {
    return await handler(result.args ?? {}, ctx);
  } catch {
    return { kind: 'failed', tool: result.tool };
  }
}
