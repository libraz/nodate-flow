/**
 * /workspaces/$id/settings/webhooks — workspace-scoped outbound webhooks
 * admin (lazy). The page lists subscriptions, lets workspace admins
 * create / delete / toggle them, send a synthetic test delivery, and
 * inspect the delivery history. See feature module for the full doc.
 */

import { createLazyFileRoute } from '@tanstack/react-router';

import WebhooksSettingsPage from '../features/webhooks/webhooks-settings-page';

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/webhooks')({
  component: WebhooksSettingsPage,
});
