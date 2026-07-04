import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('task list/spreadsheet regressions', () => {
  it('keeps InlineTitleCell hooks before the editing early return', () => {
    const source = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-list-view.tsx'),
      'utf8',
    );

    const componentStart = source.indexOf('function InlineTitleCell');
    const editingReturn = source.indexOf('if (editing) {', componentStart);
    const clickTimerHook = source.indexOf('const clickTimer = useRef', componentStart);
    const cleanupEffect = source.indexOf('Clean up pending click timer', componentStart);

    expect(componentStart).toBeGreaterThanOrEqual(0);
    expect(clickTimerHook).toBeGreaterThan(componentStart);
    expect(cleanupEffect).toBeGreaterThan(componentStart);
    expect(clickTimerHook).toBeLessThan(editingReturn);
    expect(cleanupEffect).toBeLessThan(editingReturn);
  });

  it('clears spreadsheet row selection when task filters change', () => {
    const source = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-spreadsheet-view.tsx'),
      'utf8',
    );

    expect(source).toContain('const selectionResetKey = [');
    expect(source).toContain("filters.search ?? ''");
    expect(source).toContain("filters.assigneeId ?? ''");
    expect(source).toContain('(filters.states ?? []).join');
    expect(source).toContain('(filters.priority ?? []).join');
    expect(source).toContain('setSelectedRows(new Set())');
  });
});
