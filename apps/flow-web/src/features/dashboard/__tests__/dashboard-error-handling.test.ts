/**
 * Verify that dashboard widget mutations have error handling
 * instead of silently swallowing failures.
 *
 * Bugs fixed:
 * - void updatePosition.mutateAsync() had no .catch()
 * - void deleteWidget.mutateAsync() had no .catch()
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('dashboard-view error handling', () => {
  const source = readFileSync(resolve(__dirname, '../dashboard-view.tsx'), 'utf-8');

  it('updatePosition has error handling', () => {
    // The formatter may split `updatePosition\n.mutateAsync` across lines.
    // Find the mutateAsync call for position updates specifically.
    const positionCall = source.indexOf('updatePosition\n');
    // Look further into the file from the mutation call
    const afterCall = source.slice(positionCall + 16, positionCall + 500);
    expect(afterCall).toContain('.catch(');
  });

  it('deleteWidget.mutateAsync has error handling', () => {
    const deleteCall = source.indexOf('deleteWidget.mutateAsync');
    expect(deleteCall).toBeGreaterThan(-1);

    const afterCall = source.slice(deleteCall, deleteCall + 300);
    expect(afterCall).toContain('.catch(');
  });

  it('imports toaster for error feedback', () => {
    expect(source).toContain("from '@nodate-flow/ui/primitives/toast'");
  });
});
