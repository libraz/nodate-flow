/**
 * Helpers for flag handling in the `tnk` CLI.
 *
 * These live in their own module (separate from `index.ts`) so that
 * unit tests can import them without triggering `cli.start()` at the
 * bottom of `index.ts`.
 */

import { c } from '@libraz/node-cli';

import { assertYmd, DateValidationError } from './date.js';

/**
 * Resolve the value of a flag that has both a canonical long form
 * (e.g. `--workspace-id`) and a deprecated alias (e.g. `--workspace`).
 *
 * Per the parser in `@libraz/node-cli`, long options are stored under
 * their literal name in the `options` record, so the canonical and
 * deprecated forms appear under different keys. When the user supplied
 * the deprecated form a one-line stderr warning is emitted so the
 * deprecation is discoverable without breaking existing scripts.
 *
 * If both forms are passed simultaneously, the canonical value wins
 * but the deprecation warning is still emitted.
 *
 * @param options        The action context's `options` record.
 * @param canonicalKey   Key for the preferred flag (e.g. `workspace-id`).
 * @param deprecatedKey  Key for the legacy flag (e.g. `workspace`).
 * @param canonicalFlag  Display form of the canonical flag for warnings.
 * @param deprecatedFlag Display form of the deprecated flag for warnings.
 * @param stderr         Stream the deprecation warning is written to.
 * @returns The resolved string value, or `undefined` when neither
 *          form was supplied.
 */
export function resolveDeprecatedFlag(
  options: Record<string, unknown>,
  canonicalKey: string,
  deprecatedKey: string,
  canonicalFlag: string,
  deprecatedFlag: string,
  stderr: NodeJS.WritableStream,
): string | undefined {
  const canonical = options[canonicalKey];
  const deprecated = options[deprecatedKey];
  if (typeof deprecated === 'string' && deprecated.length > 0) {
    stderr.write(
      c`{yellow Warning}: ${deprecatedFlag} is deprecated, use ${canonicalFlag} instead.\n`,
    );
    if (typeof canonical === 'string' && canonical.length > 0) {
      return canonical;
    }
    return deprecated;
  }
  if (typeof canonical === 'string' && canonical.length > 0) {
    return canonical;
  }
  return undefined;
}

/**
 * Validate an optional `YYYY-MM-DD` flag value. Returns the original
 * string when valid, `undefined` when the flag was not supplied, and
 * throws `DateValidationError` for any other input.
 *
 * @param value Raw option value (may be `undefined`).
 * @param flag  Display form of the flag for the error message.
 */
export function optionalYmd(value: unknown, flag: string): string | undefined {
  if (value === undefined) return undefined;
  return assertYmd(String(value), flag);
}

export { DateValidationError };
