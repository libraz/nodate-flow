import type { TFunction } from 'i18next';

type RecommendedAction = 'open' | 'snooze' | 'archive';

const ACTION_LABEL_KEY: Record<RecommendedAction, string> = {
  open: 'edit.action.open',
  snooze: 'edit.action.snooze',
  archive: 'edit.action.archive',
};

function isRecommendedAction(value: string): value is RecommendedAction {
  return value in ACTION_LABEL_KEY;
}

/**
 * Localised label for a suggestion's recommended action. `t` must be bound
 * to the `ai-suggestions` namespace; an unknown action is shown as-is.
 */
export function recommendedActionLabel(action: string, t: TFunction<'ai-suggestions'>): string {
  return isRecommendedAction(action) ? t(ACTION_LABEL_KEY[action]) : action;
}
