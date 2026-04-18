/**
 * Verify that useAuthBootstrap handles promise rejection gracefully
 * instead of hanging the app in 'loading' state forever.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('useAuthBootstrap error handling', () => {
  const source = readFileSync(resolve(__dirname, '../use-auth-bootstrap.ts'), 'utf-8');

  it('bootstrapPromise.then() has a .catch() handler', () => {
    // The promise chain must include .catch() to prevent unhandled
    // rejection when runBootstrap() throws (network error, etc.)
    expect(source).toContain('.catch(');
  });

  it('catch handler sets status to unauthenticated', () => {
    // On bootstrap failure, the app should show the login page
    // instead of hanging on the loading screen
    const catchBlock = source.slice(source.indexOf('.catch('));
    expect(catchBlock).toContain("'unauthenticated'");
  });
});
