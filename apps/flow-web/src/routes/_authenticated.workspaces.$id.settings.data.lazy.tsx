/**
 * /workspaces/$id/settings/data — workspace-scoped imports & exports
 * admin (lazy). The page renders an Exports card (one-shot task export
 * with format chooser) and an Imports card (job list + create form +
 * cancel) backed by the data-import / export endpoints on flow-api.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import DataSettingsPage from '../features/imports-exports/data-settings-page';

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/data')({
  component: DataSettingsPage,
});
