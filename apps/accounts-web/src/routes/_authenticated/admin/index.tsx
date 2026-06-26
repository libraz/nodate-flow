/**
 * /admin -- Redirects to /admin/users.
 */

import { createFileRoute, Navigate } from '@tanstack/react-router';

function AdminIndex() {
  return <Navigate to="/admin/users" replace />;
}

export const Route = createFileRoute('/_authenticated/admin/')({
  component: AdminIndex,
});
