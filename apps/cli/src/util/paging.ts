/**
 * Paging constants shared by every `tnk` list command.
 *
 * The list endpoints all declare `minimum:"1" maximum:"200" default:"50"`
 * on their `limit` query parameter and reject anything larger with a 422
 * instead of clamping it. `--limit` therefore means "how many rows do I
 * want in total", and the executors page for the user rather than
 * forwarding the number straight to the server.
 */

/** Largest page size the API accepts on a list endpoint. */
export const MAX_PAGE_LIMIT = 200;

/** Page size the API applies when `limit` is omitted. */
export const DEFAULT_LIST_LIMIT = 50;

/**
 * Upper bound on how many pages a single command will fetch. A server
 * that keeps returning rows without advancing would otherwise loop
 * forever.
 */
export const MAX_PAGES = 1000;

/** Normalizes a user-supplied `--limit` into a whole, non-negative count. */
export function requestedCount(limit: number): number {
  if (!Number.isFinite(limit)) return DEFAULT_LIST_LIMIT;
  return Math.max(0, Math.trunc(limit));
}

/** Narrows a requested total to a page size the API will accept. */
export function clampPageLimit(requested: number): number {
  return Math.min(Math.max(requestedCount(requested), 1), MAX_PAGE_LIMIT);
}
