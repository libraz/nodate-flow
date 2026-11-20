/**
 * WidgetBody — picks the body for a dashboard widget by its type.
 */

import type { ReactElement } from 'react';

import type { WidgetType } from '../api';
import AiSuggestionsWidget from './ai-suggestions-widget';
import BurndownWidget from './burndown-widget';
import NotificationFeedWidget from './notification-feed-widget';
import OverdueTasksWidget from './overdue-tasks-widget';
import SignalsFeedWidget from './signals-feed-widget';
import TaskSummaryWidget from './task-summary-widget';

export interface WidgetBodyProps {
  widgetType: WidgetType;
  workspaceId: string;
}

export default function WidgetBody({ widgetType, workspaceId }: WidgetBodyProps): ReactElement {
  switch (widgetType) {
    case 'task_summary':
      return <TaskSummaryWidget workspaceId={workspaceId} />;
    case 'overdue_tasks':
      return <OverdueTasksWidget workspaceId={workspaceId} />;
    case 'signals_feed':
      return <SignalsFeedWidget workspaceId={workspaceId} />;
    case 'ai_suggestions':
      return <AiSuggestionsWidget workspaceId={workspaceId} />;
    case 'notification_feed':
      return <NotificationFeedWidget workspaceId={workspaceId} />;
    case 'burndown':
      return <BurndownWidget />;
  }
}
