/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render
 * nothing (Suspense fallback would also work). On unauthenticated state
 * the user is redirected to /login.
 */

import { Outlet, createFileRoute, useNavigate, useRouterState } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';

import AppShell from '../components/layout/app-shell';
import NotFound from '../components/not-found';
import ShortcutsHelpDialog from '../components/shortcuts-help-dialog';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import AiSuggestionsDock from '../features/glass-dock/glass-dock';
import TaskCreateDialog from '../features/tasks/task-create-dialog';
import { useKeyboardShortcuts } from '../lib/use-keyboard-shortcuts';

function AuthenticatedLayout(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const navigate = useNavigate();

  // Extract projectId from current route if present.
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const projectIdMatch = pathname.match(/^\/projects\/([^/]+)/);
  const currentProjectId = projectIdMatch?.[1] ?? null;

  const [helpOpen, setHelpOpen] = useState(false);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);

  const handleCreateTask = useCallback(() => {
    if (currentProjectId) {
      setTaskDialogOpen(true);
    }
  }, [currentProjectId]);

  const handleShowHelp = useCallback(() => {
    setHelpOpen(true);
  }, []);

  useKeyboardShortcuts({
    onCreateTask: handleCreateTask,
    onShowHelp: handleShowHelp,
  });

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated) {
      void navigate({ to: '/login', replace: true });
    }
  }, [status, isAuthenticated, navigate]);

  if (status === 'loading') return null;
  if (!isAuthenticated) return null;

  return (
    <AppShell>
      <Outlet />
      <AiSuggestionsDock />
      <ShortcutsHelpDialog open={helpOpen} onClose={() => setHelpOpen(false)} />
      {currentProjectId && (
        <TaskCreateDialog
          projectId={currentProjectId}
          open={taskDialogOpen}
          onClose={() => setTaskDialogOpen(false)}
        />
      )}
    </AppShell>
  );
}

function AuthenticatedNotFound(): ReactElement {
  return (
    <AppShell>
      <NotFound />
      <AiSuggestionsDock />
    </AppShell>
  );
}

export const Route = createFileRoute('/_authenticated')({
  component: AuthenticatedLayout,
  notFoundComponent: AuthenticatedNotFound,
});
