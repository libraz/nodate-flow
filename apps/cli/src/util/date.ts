/**
 * Date validation helpers used by the `tnk` CLI.
 *
 * The product API treats `*_on` fields as `YYYY-MM-DD` strings (see
 * `docs/conventions/api-types.md`). This module rejects malformed
 * input on the client side so the user gets a clear error message
 * instead of a confusing 400 from the server.
 *
 * Per project convention (regex avoidance), parsing is done with
 * fixed-offset slicing rather than a regular expression.
 */

/**
 * Thrown by `assertYmd` when the supplied input is not a valid
 * `YYYY-MM-DD` calendar date. The CLI catches this at the top of each
 * action and exits with `EXIT_VALIDATION` (2).
 */
export class DateValidationError extends Error {
  /**
   * Create a new `DateValidationError`.
   *
   * @param message Human-readable explanation written to stderr.
   */
  constructor(message: string) {
    super(message);
    this.name = 'DateValidationError';
  }
}

/**
 * Returns true when every character of `s` between [start, end) is an
 * ASCII digit. Used as a regex-free shape check for `YYYY-MM-DD`.
 *
 * @param s     The string to test.
 * @param start Inclusive start index.
 * @param end   Exclusive end index.
 */
function isDigits(s: string, start: number, end: number): boolean {
  for (let i = start; i < end; i++) {
    const code = s.charCodeAt(i);
    if (code < 0x30 || code > 0x39) return false;
  }
  return true;
}

/**
 * Validate that `input` is a `YYYY-MM-DD` calendar date and return it
 * unchanged on success.
 *
 * Rejects:
 *   - empty strings
 *   - inputs whose shape is not `YYYY-MM-DD` (length 10, two `-` at the
 *     expected positions, digits everywhere else)
 *   - syntactically valid strings that name a non-existent day
 *     (e.g. `2030-13-45`, `2025-02-30`)
 *
 * @param input  Raw value supplied via a CLI flag.
 * @param flag   The flag name used to build the error message
 *               (e.g. `--due`).
 * @throws {DateValidationError} when `input` is not a real
 *         `YYYY-MM-DD` date.
 */
export function assertYmd(input: string, flag: string): string {
  if (input.length === 0) {
    throw new DateValidationError(`${flag} must be a YYYY-MM-DD date, got an empty value`);
  }

  // Shape check without regex: 4 digits + '-' + 2 digits + '-' + 2 digits.
  const shapeOk =
    input.length === 10 &&
    input.charAt(4) === '-' &&
    input.charAt(7) === '-' &&
    isDigits(input, 0, 4) &&
    isDigits(input, 5, 7) &&
    isDigits(input, 8, 10);
  if (!shapeOk) {
    throw new DateValidationError(`${flag} must be in YYYY-MM-DD format, got "${input}"`);
  }

  // Shape is correct; verify the components describe a real day. Using
  // individual numeric components avoids `Date.parse` quirks (which
  // would happily coerce `2025-02-30` into March 2nd).
  const year = Number(input.slice(0, 4));
  const month = Number(input.slice(5, 7));
  const day = Number(input.slice(8, 10));

  if (month < 1 || month > 12) {
    throw new DateValidationError(`${flag} has an invalid month: "${input}"`);
  }
  if (day < 1 || day > 31) {
    throw new DateValidationError(`${flag} has an invalid day: "${input}"`);
  }

  // UTC to avoid local-timezone DST or offset surprises.
  const candidate = new Date(Date.UTC(year, month - 1, day));
  if (
    candidate.getUTCFullYear() !== year ||
    candidate.getUTCMonth() !== month - 1 ||
    candidate.getUTCDate() !== day
  ) {
    throw new DateValidationError(`${flag} is not a real calendar date: "${input}"`);
  }

  return input;
}
