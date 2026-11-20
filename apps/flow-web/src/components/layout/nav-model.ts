/**
 * Single source for the app's navigation destinations. The sidebar, the
 * workspace switcher menu and the command palette all render from
 * `NAV_ENTRIES`, so a destination cannot be reachable from one and missing
 * from another.
 */

import {
  Activity,
  Archive,
  BellRing,
  Building2,
  CalendarRange,
  FileText,
  FolderKanban,
  GitCommitHorizontal,
  Home,
  Inbox,
  ListChecks,
  ListOrdered,
  type LucideIcon,
  NotebookPen,
  Settings2,
  Timer,
  UserCog,
  Users,
} from 'lucide-react';

export type NavGroup = 'personal' | 'workspace' | 'more' | 'footer' | 'switcher';

export interface NavEntry {
  key:
    | 'home'
    | 'today'
    | 'inbox'
    | 'calendar'
    | 'projects'
    | 'pages'
    | 'members'
    | 'reminders'
    | 'archive'
    | 'timeboxes'
    | 'insightsPriority'
    | 'retroDrafts'
    | 'timeline'
    | 'activity'
    | 'workspaceSettings'
    | 'settings'
    | 'allWorkspaces';
  group: NavGroup;
  icon: LucideIcon;
  /** Label key in the common namespace. */
  labelKey: `nav.${string}`;
  hintKey?: `nav.hint.${string}`;
  /** TanStack route template; `$id` is the only param. */
  route:
    | '/'
    | '/today'
    | '/inbox'
    | '/calendar'
    | '/pages'
    | '/settings/profile'
    | '/workspaces'
    | '/workspaces/$id/projects'
    | '/workspaces/$id/members'
    | '/workspaces/$id/reminders'
    | '/workspaces/$id/tasks/archived'
    | '/workspaces/$id/timeboxes'
    | '/workspaces/$id/insights/priority'
    | '/workspaces/$id/tasks/drafts'
    | '/workspaces/$id/timeline'
    | '/workspaces/$id/activity'
    | '/workspaces/$id/settings';
  /** True exactly when `route` contains `$id`. */
  needsWorkspace: boolean;
}

export const NAV_ENTRIES: readonly NavEntry[] = [
  {
    key: 'home',
    group: 'personal',
    icon: Home,
    labelKey: 'nav.home',
    route: '/',
    needsWorkspace: false,
  },
  {
    key: 'today',
    group: 'personal',
    icon: ListChecks,
    labelKey: 'nav.today',
    route: '/today',
    needsWorkspace: false,
  },
  {
    key: 'inbox',
    group: 'personal',
    icon: Inbox,
    labelKey: 'nav.inbox',
    route: '/inbox',
    needsWorkspace: false,
  },
  {
    key: 'calendar',
    group: 'personal',
    icon: CalendarRange,
    labelKey: 'nav.calendar',
    route: '/calendar',
    needsWorkspace: false,
  },
  {
    key: 'projects',
    group: 'workspace',
    icon: FolderKanban,
    labelKey: 'nav.projects',
    route: '/workspaces/$id/projects',
    needsWorkspace: true,
  },
  {
    key: 'pages',
    group: 'workspace',
    icon: FileText,
    labelKey: 'nav.pages',
    route: '/pages',
    needsWorkspace: false,
  },
  {
    key: 'members',
    group: 'workspace',
    icon: Users,
    labelKey: 'nav.members',
    route: '/workspaces/$id/members',
    needsWorkspace: true,
  },
  {
    key: 'reminders',
    group: 'workspace',
    icon: BellRing,
    labelKey: 'nav.reminders',
    hintKey: 'nav.hint.reminders',
    route: '/workspaces/$id/reminders',
    needsWorkspace: true,
  },
  {
    key: 'archive',
    group: 'workspace',
    icon: Archive,
    labelKey: 'nav.archive',
    hintKey: 'nav.hint.archive',
    route: '/workspaces/$id/tasks/archived',
    needsWorkspace: true,
  },
  {
    key: 'timeboxes',
    group: 'more',
    icon: Timer,
    labelKey: 'nav.timeboxes',
    hintKey: 'nav.hint.timeboxes',
    route: '/workspaces/$id/timeboxes',
    needsWorkspace: true,
  },
  {
    key: 'insightsPriority',
    group: 'more',
    icon: ListOrdered,
    labelKey: 'nav.insightsPriority',
    hintKey: 'nav.hint.insightsPriority',
    route: '/workspaces/$id/insights/priority',
    needsWorkspace: true,
  },
  {
    key: 'retroDrafts',
    group: 'more',
    icon: NotebookPen,
    labelKey: 'nav.retroDrafts',
    hintKey: 'nav.hint.retroDrafts',
    route: '/workspaces/$id/tasks/drafts',
    needsWorkspace: true,
  },
  {
    key: 'timeline',
    group: 'more',
    icon: GitCommitHorizontal,
    labelKey: 'nav.timeline',
    hintKey: 'nav.hint.timeline',
    route: '/workspaces/$id/timeline',
    needsWorkspace: true,
  },
  {
    key: 'activity',
    group: 'more',
    icon: Activity,
    labelKey: 'nav.activity',
    hintKey: 'nav.hint.activity',
    route: '/workspaces/$id/activity',
    needsWorkspace: true,
  },
  {
    key: 'workspaceSettings',
    group: 'footer',
    icon: Settings2,
    labelKey: 'nav.workspaceSettings',
    route: '/workspaces/$id/settings',
    needsWorkspace: true,
  },
  {
    key: 'settings',
    group: 'footer',
    icon: UserCog,
    labelKey: 'nav.settings',
    route: '/settings/profile',
    needsWorkspace: false,
  },
  {
    key: 'allWorkspaces',
    group: 'switcher',
    icon: Building2,
    labelKey: 'nav.allWorkspaces',
    route: '/workspaces',
    needsWorkspace: false,
  },
];

/** Resolve an entry to a concrete path; null when it needs a workspace and none is given. */
export function navHref(entry: NavEntry, wsId: string | null): string | null {
  if (!entry.needsWorkspace) return entry.route;
  if (wsId === null) return null;
  return entry.route.replace('$id', wsId);
}

export function navEntriesIn(group: NavGroup): readonly NavEntry[] {
  return NAV_ENTRIES.filter((e) => e.group === group);
}

export function navEntry(key: NavEntry['key']): NavEntry {
  const found = NAV_ENTRIES.find((e) => e.key === key);
  if (!found) throw new Error(`Unknown nav entry: ${key}`);
  return found;
}
