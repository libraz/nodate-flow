/**
 * TaskViewSwitcher — segmented control toggling between the task views.
 * Persists to localStorage via `useTaskView`.
 */

import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { setTaskView, type TaskView, useTaskView } from './use-task-view';

export default function TaskViewSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const view = useTaskView();

  const options: SegmentedControlOption<TaskView>[] = [
    { value: 'board', label: t('tasks.views.board') },
    { value: 'list', label: t('tasks.views.list') },
    { value: 'graph', label: t('tasks.views.graph') },
    { value: 'spreadsheet', label: t('tasks.views.spreadsheet') },
  ];

  return (
    <SegmentedControl<TaskView>
      value={view}
      onChange={setTaskView}
      options={options}
      ariaLabel={t('tasks.title')}
    />
  );
}
