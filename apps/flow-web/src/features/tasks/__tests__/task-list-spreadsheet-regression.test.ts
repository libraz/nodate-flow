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
    const draftHook = source.indexOf('const [draft, setDraft] = useState', componentStart);
    const focusEffect = source.indexOf('Reset draft and focus', componentStart);

    expect(componentStart).toBeGreaterThanOrEqual(0);
    expect(draftHook).toBeGreaterThan(componentStart);
    expect(focusEffect).toBeGreaterThan(componentStart);
    expect(draftHook).toBeLessThan(editingReturn);
    expect(focusEffect).toBeLessThan(editingReturn);
    // A plain click navigates through the link; no delayed-click timer is left behind.
    expect(source).not.toContain('clickTimer');
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
