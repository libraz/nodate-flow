/**
 * /admin -- Redirects to /admin/users.
 */

import { Navigate, createFileRoute } from '@tanstack/react-router';

function AdminIndex() {
  return <Navigate to="/admin/users" replace />;
}

export const Route = createFileRoute('/_authenticated/admin/')({
  component: AdminIndex,
});
