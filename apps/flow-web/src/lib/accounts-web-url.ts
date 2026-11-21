/** Base URL of accounts-web (identity UI), without a trailing slash. */
export const ACCOUNTS_WEB_BASE = (
  (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ?? 'http://localhost:5175/accounts'
).replace(/\/+$/, '');

/** Absolute URL of an accounts-web page; `path` starts with `/`. */
export function accountsWebUrl(path: string): string {
  return `${ACCOUNTS_WEB_BASE}${path}`;
}
