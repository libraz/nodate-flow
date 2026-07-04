/**
 * @brief Tiny client-side error logger.
 *
 * `console.error` from page code leaks raw stack traces and SDK
 * details into end-user devtools. That is fine during development —
 * we want loud feedback while iterating — but on a production build
 * the details are noise at best and weak information disclosure at
 * worst (auth flows, request IDs, paths the user never sees in the
 * UI). This helper centralises the gate so we have exactly one
 * place to wire a real error reporter into later, without sweeping
 * every call site again.
 *
 * Today the implementation is intentionally minimal:
 *   - In dev (`import.meta.env.DEV`), forward to `console.error`
 *     verbatim so existing dev workflows behave the same.
 *   - In all other modes, swallow silently.
 *
 * No centralised reporter exists in the codebase yet; the audit
 * recommendation was explicitly "search first; don't add new infra"
 * (see TASK #13 / B12). When one is introduced, this file is the
 * single seam to plug it into.
 *
 * @param message Short human-readable label describing the failure
 *                site. Treated as a stable prefix in dev output.
 * @param err Optional underlying error / unknown thrown value.
 */
export function logError(message: string, err?: unknown): void {
  if (import.meta.env.DEV) {
    // biome-ignore lint/suspicious/noConsole: dev-only centralized logger intentionally forwards to console.
    console.error(`[accounts-web] ${message}`, err);
  }
}
