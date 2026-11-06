import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';
import { recommendedActionLabel } from '../recommended-action-label';

const t = ((key: string) => `t:${key}`) as unknown as TFunction<'ai-suggestions'>;

describe('recommendedActionLabel', () => {
  it('translates each known action through its literal key', () => {
    expect(recommendedActionLabel('open', t)).toBe('t:edit.action.open');
    expect(recommendedActionLabel('snooze', t)).toBe('t:edit.action.snooze');
    expect(recommendedActionLabel('archive', t)).toBe('t:edit.action.archive');
  });

  it('shows an unknown action as-is', () => {
    expect(recommendedActionLabel('delete', t)).toBe('delete');
  });
});
