/**
 * TaskViewSwitcher — segmented control toggling between the task views.
 * Persists to localStorage via `useTaskView`. Each segment pairs an icon with
 * its label, and the tooltip says what sets the views apart.
 */

import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Columns3, List, type LucideIcon, Network, Sheet } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './task-view-switcher.module.css';
import { setTaskView, type TaskView, useTaskView } from './use-task-view';

function SegmentLabel({ icon: IconCmp, text }: { icon: LucideIcon; text: string }): ReactElement {
  return (
    <span className={styles.segment}>
      <IconCmp size={14} aria-hidden="true" />
      <span>{text}</span>
    </span>
  );
}

export default function TaskViewSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const view = useTaskView();

  const board = t('tasks.views.board');
  const list = t('tasks.views.list');
  const graph = t('tasks.views.graph');
  const spreadsheet = t('tasks.views.spreadsheet');
  const options: SegmentedControlOption<TaskView>[] = [
    { value: 'board', label: <SegmentLabel icon={Columns3} text={board} />, ariaLabel: board },
    { value: 'list', label: <SegmentLabel icon={List} text={list} />, ariaLabel: list },
    { value: 'graph', label: <SegmentLabel icon={Network} text={graph} />, ariaLabel: graph },
    {
      value: 'spreadsheet',
      label: <SegmentLabel icon={Sheet} text={spreadsheet} />,
      ariaLabel: spreadsheet,
    },
  ];

  // The wrapper takes the tooltip's hover/focus listeners; focus bubbles from the segments.
  return (
    <Tooltip content={t('tasks.views_hint')} placement="bottom">
      <span className={styles.root}>
        <SegmentedControl<TaskView>
          value={view}
          onChange={setTaskView}
          options={options}
          ariaLabel={t('tasks.views_label')}
        />
      </span>
    </Tooltip>
  );
}
