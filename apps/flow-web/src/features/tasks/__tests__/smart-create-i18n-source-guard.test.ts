import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('smart-create subtask i18n guards', () => {
  it('does not render raw AI priority labels directly', () => {
    const src = readFileSync(
      join(process.cwd(), 'src/features/tasks/task-create-dialog.tsx'),
      'utf8',
    );

    expect(src).not.toContain(
      '<Badge tone={priorityTone(subtask.priority)}>{subtask.priority}</Badge>',
    );
    expect(src).toContain("t('tasks.steps.priority_low')");
    expect(src).toContain("t('tasks.steps.priority_medium')");
    expect(src).toContain("t('tasks.steps.priority_high')");
    expect(src).toContain("t('tasks.smart_create.priority_urgent')");
  });
});
