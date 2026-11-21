/**
 * Catch-all under the authenticated layout: an unknown URL renders the 404
 * inside the app shell the layout already provides. Every concrete route
 * outranks a splat, so this only receives paths nothing else matches.
 */

import { createFileRoute } from '@tanstack/react-router';

import NotFound from '../components/not-found';

export const Route = createFileRoute('/_authenticated/$')({
  component: NotFound,
});
