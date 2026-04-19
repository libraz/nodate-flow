/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render
 * nothing (Suspense fallback would also work). On unauthenticated state
 * the user is redirected to /login.
 */

import { Outlet, createFileRoute, useRouterState } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';

/**
 * isSafeRedirect returns true when the URL is a relative path (starting
 * with a single slash) or points to the same origin as the current page.
 * Protocol-relative URLs ("//evil.com") and foreign origins are rejected
 * to prevent open-redirect attacks.
 */
function isSafeRedirect(url: string): boolean {
  if (url.startsWith('/') && !url.startsWith('//')) return true;
  try {
    const parsed = new URL(url);
    return parsed.origin === window.location.origin;
  } catch {
    return false;
  }
}

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
      const accountsUrl =
        (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ?? 'http://localhost:5175';
      const target = `${accountsUrl}/login?redirect=${encodeURIComponent(window.location.href)}`;
      if (isSafeRedirect(target)) {
        window.location.href = target;
      }
    }
  }, [status, isAuthenticated]);

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
