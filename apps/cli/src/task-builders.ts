/**
 * Pure builders and executors for `tnk task` subcommands.
 *
 * The CLI command actions in `index.ts` register options with
 * `@libraz/node-cli` and forward to these helpers. Splitting the
 * SDK-call shape and option-validation into pure functions keeps the
 * unit tests independent from the CLI parser.
 */

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
  patchBody?: Record<string, unknown>;
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
  const patchBody: Record<string, unknown> = {};
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

/**
 * Minimal SDK surface used by the executors below. Method names are
 * uppercase to match the openapi-fetch / SDK shape; the linter's
 * camelCase rule is intentionally suppressed for this contract.
 */
export interface SdkClientLike {
  // biome-ignore lint/style/useNamingConvention: SDK method name
  // biome-ignore lint/suspicious/noExplicitAny: matches openapi-fetch dynamic shape
  GET: (url: string, opts: any) => Promise<{ data?: unknown; error?: unknown }>;
  // biome-ignore lint/style/useNamingConvention: SDK method name
  // biome-ignore lint/suspicious/noExplicitAny: matches openapi-fetch dynamic shape
  POST: (url: string, opts: any) => Promise<{ data?: unknown; error?: unknown }>;
  // biome-ignore lint/style/useNamingConvention: SDK method name
  // biome-ignore lint/suspicious/noExplicitAny: matches openapi-fetch dynamic shape
  PATCH: (url: string, opts: any) => Promise<{ data?: unknown; error?: unknown }>;
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
