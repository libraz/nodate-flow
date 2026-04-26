/**
 * AIAgentsEmpty — placeholder shown when the task has no recorded AI
 * invocations yet. Kept intentionally quiet so the section recedes
 * until an agent actually does something against the task.
 *
 * Thin wrapper around the shared {@link EmptyState} primitive.
 */

import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export default function AIAgentsEmpty(): ReactElement {
  const { t } = useTranslation('aiAgents');
  return <EmptyState title={t('empty.title')} description={t('empty.body')} />;
}
