import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('L-group-3 source guards', () => {
  it('keeps spreadsheet commits keyed by the task id captured at edit start', () => {
    const source = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-spreadsheet-view.tsx'),
      'utf8',
    );

    expect(source).toContain('taskId: string');
    expect(source).toContain('setEditingCell({ rowIdx, taskId: task.id, column })');
    expect(source).toContain('editingCell.taskId');
  });

  it('keeps smart-create subtask checkbox ids independent of proposal titles', () => {
    const source = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-create-dialog.tsx'),
      'utf8',
    );

    expect(source).toContain('const baseId = useId()');
    expect(source).toContain('const checkboxId = `');
    expect(source).toMatch(/-subtask-\$\{index\}`/);
    expect(source).not.toContain("subtask.title.replaceAll(/\\s+/g, '-').slice(0, 32)");
  });
});
