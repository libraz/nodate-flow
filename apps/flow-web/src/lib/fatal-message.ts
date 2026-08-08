/**
 * Decides what detail text, if any, the root error boundary may show.
 *
 * A thrown error's `message` is written for whoever is reading a stack
 * trace: SDK internals, request paths, auth-flow state. `lib/log.ts` already
 * refuses to forward that to the console outside dev on information-
 * disclosure grounds, and painting the same string into the page would undo
 * it — in an untranslated English `<pre>`, on a screen the reader cannot act
 * on anyway.
 *
 * So the rule is: show the message only when the error catalog can name it
 * in the reader's language. Everything else is detail for developers, and is
 * withheld from a production build.
 */

import { ApiError } from '@nodate-flow/sdk';

/** Minimal shape of the i18next `t` used here. */
export type TranslateErrorCode = (code: string) => string;

/**
 * @param error - The value the boundary caught.
 * @param translate - Resolves an error code against the `errors` namespace,
 * returning `''` when the catalog has no entry.
 * @param isDev - Whether this is a development build.
 * @returns Text safe to render, or `''` when there is nothing to show.
 */
export function fatalDetailMessage(
  error: unknown,
  translate: TranslateErrorCode,
  isDev: boolean,
): string {
  if (error instanceof ApiError && error.code) {
    const named = translate(error.code);
    if (named) return named;
  }
  if (!isDev) return '';
  if (error instanceof Error) return error.message;
  return String(error);
}
