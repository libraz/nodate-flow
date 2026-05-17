/**
 * Unit tests for the structured exit-code policy in `src/util/exit.ts`.
 *
 * The constants themselves are trivial; the interesting behaviour is
 * `isAuthRequiredError`, which detects both `AuthRequiredError`
 * instances and the plain-`Error` shape that `createFlowClient`
 * throws when no credentials are stored on disk.
 */

import { describe, expect, it } from 'vitest';

import {
  AuthRequiredError,
  EXIT_AUTH,
  EXIT_RUNTIME,
  EXIT_VALIDATION,
  isAuthRequiredError,
} from '../src/util/exit.js';

describe('exit code constants', () => {
  it('uses 1 for runtime errors', () => {
    expect(EXIT_RUNTIME).toBe(1);
  });

  it('uses 2 for validation errors', () => {
    expect(EXIT_VALIDATION).toBe(2);
  });

  it('uses 3 for auth errors', () => {
    expect(EXIT_AUTH).toBe(3);
  });

  it('keeps the three codes distinct', () => {
    const codes = new Set<number>([EXIT_RUNTIME, EXIT_VALIDATION, EXIT_AUTH]);
    expect(codes.size).toBe(3);
  });
});

describe('isAuthRequiredError', () => {
  it('recognises the dedicated AuthRequiredError class', () => {
    expect(isAuthRequiredError(new AuthRequiredError())).toBe(true);
    expect(isAuthRequiredError(new AuthRequiredError('custom'))).toBe(true);
  });

  it('recognises the plain Error thrown by createFlowClient when not logged in', () => {
    const err = new Error('Not logged in. Run `tnk auth login` to authenticate first.');
    expect(isAuthRequiredError(err)).toBe(true);
  });

  it('does not match unrelated errors', () => {
    expect(isAuthRequiredError(new Error('boom'))).toBe(false);
    expect(isAuthRequiredError(new TypeError('bad'))).toBe(false);
  });

  it('does not match non-error throws', () => {
    expect(isAuthRequiredError(undefined)).toBe(false);
    expect(isAuthRequiredError(null)).toBe(false);
    expect(isAuthRequiredError('Not logged in')).toBe(false);
    expect(isAuthRequiredError(42)).toBe(false);
  });
});

/**
 * Behaviour test: when an action handler catches an auth failure
 * raised by `createFlowClient()` it should set `process.exitCode` to
 * `EXIT_AUTH`, and when it catches a date-validation failure it
 * should set it to `EXIT_VALIDATION`. We model the relevant lines of
 * `src/index.ts` directly here to keep the test independent from
 * `@libraz/node-cli`'s runtime.
 */
describe('action error handling shape', () => {
  it('maps a missing-credentials throw to EXIT_AUTH', () => {
    const previous = process.exitCode;
    process.exitCode = 0;
    try {
      try {
        throw new Error('Not logged in. Run `tnk auth login` to authenticate first.');
      } catch (err) {
        if (isAuthRequiredError(err)) {
          process.exitCode = EXIT_AUTH;
        } else {
          process.exitCode = EXIT_RUNTIME;
        }
      }
      expect(process.exitCode).toBe(EXIT_AUTH);
    } finally {
      process.exitCode = previous;
    }
  });

  it('maps a date-validation throw to EXIT_VALIDATION', async () => {
    const { DateValidationError, assertYmd } = await import('../src/util/date.js');
    const previous = process.exitCode;
    process.exitCode = 0;
    try {
      try {
        assertYmd('2030-13-45', '--due');
      } catch (err) {
        if (err instanceof DateValidationError) {
          process.exitCode = EXIT_VALIDATION;
        } else {
          process.exitCode = EXIT_RUNTIME;
        }
      }
      expect(process.exitCode).toBe(EXIT_VALIDATION);
    } finally {
      process.exitCode = previous;
    }
  });
});
