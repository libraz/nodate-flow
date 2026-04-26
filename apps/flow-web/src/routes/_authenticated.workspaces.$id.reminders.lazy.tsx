/**
 * /workspaces/$id/reminders — workspace-scoped reminders surface
 * (lazy). Renders every reminder produced by the deterministic
 * reminder engine, grouped by urgency. Read-only — no snooze or
 * dismiss endpoints exist on flow-api today.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import RemindersPage from '../features/reminders/reminders-page';

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/reminders')({
  component: RemindersPage,
});
