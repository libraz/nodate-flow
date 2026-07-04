/**
 * Source guard for M-13: task mutation error toasts should preserve API error
 * codes/details through `formatApiError` instead of swallowing them behind a
 * fixed translated fallback.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

const TASK_ERROR_TOAST_FILES = [
  '../task-create-dialog.tsx',
  '../quick-capture-dialog.tsx',
  '../task-attachments.tsx',
  '../comment-row.tsx',
  '../task-steps-panel.tsx',
  '../task-list-view.tsx',
  '../task-spreadsheet-view.tsx',
  '../agent-panel/agent-panel.tsx',
  '../../../routes/_authenticated.tasks.$taskId.lazy.tsx',
] as const;

describe('task mutation error toasts', () => {
  it('formats caught API errors instead of using fixed fallback-only messages', () => {
    for (const file of TASK_ERROR_TOAST_FILES) {
      const source = readFileSync(resolve(__dirname, file), 'utf8');
      expect(source, file).toContain('formatApiError');
      expect(source, file).not.toMatch(/catch\s*\{\s*toaster\.show/);
      expect(source, file).not.toMatch(/\.catch\(\(\)\s*=>\s*\{\s*toaster\.show/);
      expect(source, file).not.toMatch(/onError:\s*\(\)\s*=>\s*\{\s*toaster\.show/);
    }
  });
});
