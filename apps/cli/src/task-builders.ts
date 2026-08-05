/**
 * Pure builders and executors for `tnk task` subcommands.
 *
 * The CLI command actions in `index.ts` register options with
 * `@libraz/node-cli` and forward to these helpers. Splitting the
 * SDK-call shape and option-validation into pure functions keeps the
 * unit tests independent from the CLI parser.
 */

import type { components, NodateFlowClient } from '@nodate-flow/sdk';

import { clampPageLimit, MAX_PAGES, requestedCount } from './util/paging.js';

/* ── State transition helpers ──────────────────────────────────── */

/**
 * Allowed values for `--state` on `tnk task update`. Each value maps
 * to a state-machine transition accepted by
 * `POST /tasks/{id}/transitions`. The list mirrors the
 * `TransitionTaskBody.transition` enum in the OpenAPI spec.
 */
export const STATE_TRANSITIONS = [
  'start',
  'block',
  'unblock',
  'submit',
  'complete',
  'reopen',
  'cancel',
] as const;
export type StateTransition = (typeof STATE_TRANSITIONS)[number];

export const MIN_PRIORITY = 0;
export const MAX_PRIORITY = 4;

export function isValidPriority(value: unknown): value is number {
  return (
    typeof value === 'number' &&
    Number.isInteger(value) &&
    value >= MIN_PRIORITY &&
    value <= MAX_PRIORITY
  );
}

/* ── Update builder ────────────────────────────────────────────── */

/** Raw options accepted by `tnk task update <id>`. */
export interface UpdateOptions {
  title?: string;
  description?: string;
  /** YYYY-MM-DD or empty string to clear. */
  due?: string;
  /** YYYY-MM-DD or empty string to clear. */
  start?: string;
  priority?: number;
  state?: string;
  visibility?: 'public' | 'project' | 'private';
}

/** Resolved request shape for the patch + transition steps. */
export interface UpdatePlan {
  /** Body for `PATCH /tasks/{id}`; absent when no patch fields were given. */
  patchBody?: components['schemas']['PatchTaskBody'];
  /** Transition to issue against `POST /tasks/{id}/transitions`. */
  stateTransition?: StateTransition;
}

/**
 * Build a structured request plan from raw `tnk task update` options.
 *
 * Returns `undefined` when the user passed no actionable flag, so the
 * caller can surface the "no fields to update" error.
 */
export function buildUpdatePlan(options: UpdateOptions): UpdatePlan | undefined {
  const patchBody: components['schemas']['PatchTaskBody'] = {};
  if (options.title !== undefined) patchBody.title = options.title;
  if (options.description !== undefined) patchBody.description = options.description;
  if (options.due !== undefined) patchBody.dueOn = options.due;
  if (options.start !== undefined) patchBody.startOn = options.start;
  if (options.priority !== undefined) patchBody.priority = options.priority;
  if (options.visibility !== undefined) patchBody.visibility = options.visibility;

  const stateTransition = isStateTransition(options.state) ? options.state : undefined;

  if (Object.keys(patchBody).length === 0 && stateTransition === undefined) {
    return undefined;
  }

  const plan: UpdatePlan = {};
  if (Object.keys(patchBody).length > 0) plan.patchBody = patchBody;
  if (stateTransition !== undefined) plan.stateTransition = stateTransition;
  return plan;
}

/** Type guard for `--state` values. */
export function isStateTransition(value: unknown): value is StateTransition {
  return typeof value === 'string' && (STATE_TRANSITIONS as readonly string[]).includes(value);
}

/* ── Search builder ────────────────────────────────────────────── */

/** Raw options accepted by `tnk task search <query>`. */
export interface SearchOptions {
  workspaceId?: string;
  projectId?: string;
  limit?: number;
}

/** Resolved querystring shape for `GET /tasks`. */
export interface SearchQuery {
  q: string;
  limit: number;
  offset?: number;
  cursor?: string;
  workspaceId?: string;
  projectId?: string;
}

/** Reasons `buildSearchQuery` may reject the user's input. */
export type SearchValidationError = 'empty_query' | 'missing_scope';

/**
 * Build the GET /tasks query parameters from raw `tnk task search`
 * options. Validates that the query is non-empty and that the search
 * is scoped to either a workspace or a project; returns a tagged error
 * value otherwise so the caller can render it.
 */
export function buildSearchQuery(
  rawQuery: string,
  options: SearchOptions,
): SearchQuery | SearchValidationError {
  const trimmed = rawQuery.trim();
  if (trimmed.length === 0) return 'empty_query';
  if (!options.workspaceId && !options.projectId) return 'missing_scope';

  const query: SearchQuery = {
    q: trimmed,
    limit: options.limit ?? 20,
  };
  if (options.workspaceId) query.workspaceId = options.workspaceId;
  if (options.projectId) query.projectId = options.projectId;
  return query;
}

/* ── SDK-shape helpers ─────────────────────────────────────────── */

/** Minimal typed SDK surface used by the executors below. */
export type SdkClientLike = Pick<NodateFlowClient, 'GET' | 'POST' | 'PATCH'>;

export interface TaskListQuery {
  limit: number;
  offset?: number;
  cursor?: string;
  workspaceId?: string;
  projectId?: string;
  q?: string;
  state?: string[];
}

export type TaskListPage = components['schemas']['ListTasksBody'];

export interface TaskListResult {
  tasks: unknown[];
  total: number;
}

async function executeTaskListPage(
  client: SdkClientLike,
  query: TaskListQuery,
): Promise<{ data?: TaskListPage; error?: unknown }> {
  return client.GET('/tasks', { params: { query } }) as Promise<{
    data?: TaskListPage;
    error?: unknown;
  }>;
}

/**
 * Fetch every page for list/search commands. The backend can return a
 * `nextCursor` on unfiltered first pages; filtered paths may only expose
 * `total`, so this falls back to offset paging when no cursor is present.
 *
 * `query.limit` is how many tasks the caller wants in total, which may
 * exceed what `GET /tasks` serves in one page. The per-page limit is
 * clamped to `MAX_PAGE_LIMIT` — the endpoint answers a larger value with
 * a 422 rather than clamping it itself — and pages accumulate until the
 * requested count is reached.
 */
export async function executeTaskListPaginated(
  client: SdkClientLike,
  query: TaskListQuery,
): Promise<{ data?: TaskListResult; error?: unknown }> {
  const tasks: unknown[] = [];
  const requestedLimit = requestedCount(query.limit);
  if (requestedLimit === 0) return { data: { tasks: [], total: 0 } };

  let total: number | undefined;
  let nextQuery: TaskListQuery = {
    ...query,
    limit: clampPageLimit(requestedLimit),
    offset: query.offset ?? 0,
  };
  let lastCursor: string | undefined;

  for (let page = 0; page < MAX_PAGES; page += 1) {
    const result = await executeTaskListPage(client, nextQuery);
    if (result.error) return { error: result.error };

    const pageTasks = Array.isArray(result.data?.tasks) ? result.data.tasks : [];
    tasks.push(...pageTasks);
    if (typeof result.data?.total === 'number') {
      total = result.data.total;
    }
    if (tasks.length >= requestedLimit) {
      return { data: { tasks: tasks.slice(0, requestedLimit), total: total ?? tasks.length } };
    }

    const nextCursor =
      typeof result.data?.nextCursor === 'string' && result.data.nextCursor.length > 0
        ? result.data.nextCursor
        : undefined;
    if (nextCursor) {
      if (nextCursor === lastCursor) break;
      lastCursor = nextCursor;
      nextQuery = { ...nextQuery, cursor: nextCursor };
      delete nextQuery.offset;
      continue;
    }

    if (total !== undefined && tasks.length < total && pageTasks.length > 0) {
      nextQuery = { ...nextQuery, offset: (nextQuery.offset ?? 0) + pageTasks.length };
      delete nextQuery.cursor;
      continue;
    }

    break;
  }

  return { data: { tasks, total: total ?? tasks.length } };
}

/**
 * Issue the patch + transition calls described by `plan`. The latest
 * task representation seen on either response is returned to the
 * caller for printing.
 */
export async function executeUpdate(
  client: SdkClientLike,
  taskId: string,
  plan: UpdatePlan,
): Promise<{ data?: unknown; error?: unknown }> {
  let latest: unknown;

  if (plan.patchBody) {
    const result = await client.PATCH('/tasks/{id}', {
      params: { path: { id: taskId } },
      body: plan.patchBody,
    });
    if (result.error) return { error: result.error };
    latest = result.data;
  }

  if (plan.stateTransition) {
    const result = await client.POST('/tasks/{id}/transitions', {
      params: { path: { id: taskId } },
      body: { transition: plan.stateTransition },
    });
    if (result.error) return { error: result.error };
    latest = result.data;
  }

  return { data: latest };
}

/**
 * Issue `GET /tasks?q=...` with the resolved search query. Thin
 * wrapper exposed for symmetry with `executeUpdate`.
 */
export async function executeSearch(
  client: SdkClientLike,
  query: SearchQuery,
): Promise<{ data?: unknown; error?: unknown }> {
  return client.GET('/tasks', { params: { query } });
}

export async function executeSearchPaginated(
  client: SdkClientLike,
  query: SearchQuery,
): Promise<{ data?: TaskListResult; error?: unknown }> {
  return executeTaskListPaginated(client, query);
}
