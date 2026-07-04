import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('TaskListView drag handle accessibility', () => {
  it('keeps the reorder handle keyboard-operable', () => {
    const source = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-list-view.tsx'),
      'utf8',
    );

    expect(source).toContain('onKeyDown={(e) => handleReorderKeyDown(e, row.index)}');
    expect(source).toContain('aria-keyshortcuts="ArrowUp ArrowDown"');
    expect(source).toContain("e.key === 'ArrowUp'");
    expect(source).toContain("e.key === 'ArrowDown'");
  });
});
