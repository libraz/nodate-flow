/**
 * Grouping the diff into blocks is what lets the drawer hand whole runs
 * to the body renderer. It may not move a line to the other side of the
 * diff while doing so.
 */

import { describe, expect, it } from 'vitest';

import { diffLines, groupDiffLines } from '../diff';

describe('grouping a line diff into blocks', () => {
  it('keeps consecutive lines of one side together', () => {
    const blocks = groupDiffLines(diffLines('a\nb\nc', 'a'));

    expect(blocks).toEqual([
      { op: 'equal', text: 'a' },
      { op: 'added', text: 'b\nc' },
    ]);
  });

  it('never merges lines that belong to different sides', () => {
    const blocks = groupDiffLines(diffLines('old', 'new'));

    expect(blocks).toEqual([
      { op: 'added', text: 'old' },
      { op: 'removed', text: 'new' },
    ]);
  });

  it('carries every line through in the order it was diffed', () => {
    const lines = diffLines('one\ntwo\nthree', 'one\nTWO\nthree');
    const blocks = groupDiffLines(lines);

    expect(blocks.flatMap((block) => block.text.split('\n'))).toEqual(
      lines.map((line) => line.text),
    );
  });

  it('reads a changed mention target as a change', () => {
    const before = '@[Ann Rivers](user:019649b0-0000-7000-8000-000000000000)';
    const after = '@[Ann Rivers](user:019649b0-0000-7000-8000-000000000001)';

    expect(groupDiffLines(diffLines(before, after)).every((b) => b.op === 'equal')).toBe(false);
  });
});
