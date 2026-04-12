/**
 * TaskViewSwitcher — segmented control toggling between Board and List
 * views. Persists to localStorage via `useTaskView`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { setTaskView, useTaskView } from './use-task-view';

export default function TaskViewSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const view = useTaskView();

  return (
    <div
      role="tablist"
      aria-label={t('tasks.title')}
      style={{ display: 'inline-flex', gap: '0.25rem' }}
    >
      <Button
        type="button"
        role="tab"
        aria-selected={view === 'board'}
        variant={view === 'board' ? 'primary' : 'ghost'}
        onClick={() => {
          setTaskView('board');
        }}
      >
        {t('tasks.views.board')}
      </Button>
      <Button
        type="button"
        role="tab"
        aria-selected={view === 'list'}
        variant={view === 'list' ? 'primary' : 'ghost'}
        onClick={() => {
          setTaskView('list');
        }}
      >
        {t('tasks.views.list')}
      </Button>
      <Button
        type="button"
        role="tab"
        aria-selected={view === 'graph'}
        variant={view === 'graph' ? 'primary' : 'ghost'}
        onClick={() => {
          setTaskView('graph');
        }}
      >
        {t('tasks.views.graph')}
      </Button>
    </div>
  );
}
