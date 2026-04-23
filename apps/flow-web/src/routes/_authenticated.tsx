/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render
 * nothing (Suspense fallback would also work). On unauthenticated state
 * the user is redirected to /login.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import { Outlet, createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';

import AppShell from '../components/layout/app-shell';
import {
  OPEN_COMMAND_PALETTE_EVENT,
  OPEN_CREATE_TASK_EVENT,
  OPEN_QUICK_CAPTURE_EVENT,
} from '../components/layout/glass-dock';
import NotFound from '../components/not-found';
import ShortcutsHelpDialog from '../components/shortcuts-help-dialog';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import AiSuggestionsDock from '../features/glass-dock/glass-dock';
import QuickCaptureDialog from '../features/tasks/quick-capture-dialog';
import TaskCreateDialog from '../features/tasks/task-create-dialog';
import { useDefaultProjectId } from '../lib/use-default-project';
import { useKeyboardShortcuts } from '../lib/use-keyboard-shortcuts';

function AuthenticatedLayout(): ReactElement | null {
  const { status } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  // Resolve the default project for FAB-triggered task creation. This
  // falls back beyond the URL so "new task" and "quick capture" remain
  // usable from `/today`, `/inbox`, the home page, etc.
  const { projectId: defaultProjectId, workspaceId: defaultWorkspaceId } = useDefaultProjectId();

  const [helpOpen, setHelpOpen] = useState(false);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);
  const [quickCaptureOpen, setQuickCaptureOpen] = useState(false);

  // Open the full task-creation dialog. When no project is reachable we
  // fall back to the command palette so the user sees the "create a
  // project first" UX rather than a dead button.
  const handleCreateTask = useCallback(() => {
    if (defaultProjectId) {
      setTaskDialogOpen(true);
      return;
    }
    window.dispatchEvent(new CustomEvent(OPEN_COMMAND_PALETTE_EVENT));
  }, [defaultProjectId]);

  const handleQuickCapture = useCallback(() => {
    if (defaultProjectId) {
      setQuickCaptureOpen(true);
      return;
    }
    window.dispatchEvent(new CustomEvent(OPEN_COMMAND_PALETTE_EVENT));
  }, [defaultProjectId]);

  const handleShowHelp = useCallback(() => {
    setHelpOpen(true);
  }, []);

  useKeyboardShortcuts({
    onCreateTask: handleCreateTask,
    onShowHelp: handleShowHelp,
  });

  // Bridge FAB button clicks (window CustomEvents) to the layout-level
  // dialog open state. Keeping a single dialog instance here means the
  // keyboard shortcut and the FAB share one mounted dialog tree.
  useEffect(() => {
    const createListener = (): void => {
      handleCreateTask();
    };
    const quickListener = (): void => {
      handleQuickCapture();
    };
    window.addEventListener(OPEN_CREATE_TASK_EVENT, createListener);
    window.addEventListener(OPEN_QUICK_CAPTURE_EVENT, quickListener);
    return () => {
      window.removeEventListener(OPEN_CREATE_TASK_EVENT, createListener);
      window.removeEventListener(OPEN_QUICK_CAPTURE_EVENT, quickListener);
    };
  }, [handleCreateTask, handleQuickCapture]);

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
      {defaultProjectId && (
        <>
          <TaskCreateDialog
            projectId={defaultProjectId}
            {...(defaultWorkspaceId ? { workspaceId: defaultWorkspaceId } : {})}
            open={taskDialogOpen}
            onClose={() => setTaskDialogOpen(false)}
          />
          <QuickCaptureDialog
            projectId={defaultProjectId}
            open={quickCaptureOpen}
            onClose={() => setQuickCaptureOpen(false)}
          />
        </>
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
