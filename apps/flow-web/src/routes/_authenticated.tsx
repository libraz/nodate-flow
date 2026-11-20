/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render a
 * quiet skeleton that only fades in if the probe is slow. On unauthenticated state
 * the user is redirected to /login.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import { createFileRoute, Outlet } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AppShell from '../components/layout/app-shell';
import { OPEN_CREATE_TASK_EVENT, OPEN_QUICK_CAPTURE_EVENT } from '../components/layout/glass-dock';
import NotFound from '../components/not-found';
import { OPEN_SHORTCUTS_HELP_EVENT } from '../components/page-help';
import ShortcutsHelpDialog from '../components/shortcuts-help-dialog';
import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { useAuthBootstrap } from '../features/auth/use-auth-bootstrap';
import AiSuggestionsDock from '../features/glass-dock/glass-dock';
import NoProjectHintDialog from '../features/tasks/no-project-hint-dialog';
import QuickCaptureDialog from '../features/tasks/quick-capture-dialog';
import TaskCreateDialog from '../features/tasks/task-create-dialog';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../lib/use-current-workspace-role';
import { useDefaultProjectId } from '../lib/use-default-project';
import { useKeyboardShortcuts } from '../lib/use-keyboard-shortcuts';
import styles from './_authenticated.module.css';

function AuthenticatedLayout(): ReactElement | null {
  const { status, retry } = useAuthBootstrap();
  const isAuthenticated = useAuth(selectIsAuthenticated);

  // Resolve the default project for FAB-triggered task creation. This
  // falls back beyond the URL so "new task" and "quick capture" remain
  // usable from `/today`, `/inbox`, the home page, etc.
  const { projectId: defaultProjectId, workspaceId: defaultWorkspaceId } = useDefaultProjectId();

  const [helpOpen, setHelpOpen] = useState(false);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);
  const [quickCaptureOpen, setQuickCaptureOpen] = useState(false);
  // Shown when the user triggers task creation from a FAB/shortcut while
  // no project is reachable in the active workspace. Mirrors the calendar
  // quick-create flow's inline "create a project first" guidance so that
  // the CTA is never a dead button.
  const [noProjectHintOpen, setNoProjectHintOpen] = useState(false);
  // Project created from the hint dialog; wins over the resolved default
  // until the task dialog closes, since the default may not have refetched.
  const [createdProjectId, setCreatedProjectId] = useState<string | null>(null);
  const canCreateProject = isWorkspaceAdmin(useCurrentWorkspaceRole());

  // Open the full task-creation dialog. When no project is reachable we
  // instead prompt the user to create a project first so the CTA is never
  // a dead button.
  const handleCreateTask = useCallback(() => {
    if (defaultProjectId) {
      setTaskDialogOpen(true);
      return;
    }
    setNoProjectHintOpen(true);
  }, [defaultProjectId]);

  const handleQuickCapture = useCallback(() => {
    if (defaultProjectId) {
      setQuickCaptureOpen(true);
      return;
    }
    setNoProjectHintOpen(true);
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
    window.addEventListener(OPEN_SHORTCUTS_HELP_EVENT, handleShowHelp);
    return () => {
      window.removeEventListener(OPEN_CREATE_TASK_EVENT, createListener);
      window.removeEventListener(OPEN_QUICK_CAPTURE_EVENT, quickListener);
      window.removeEventListener(OPEN_SHORTCUTS_HELP_EVENT, handleShowHelp);
    };
  }, [handleCreateTask, handleQuickCapture, handleShowHelp]);

  useEffect(() => {
    if (status === 'unauthenticated' && !isAuthenticated) {
      const accountsUrl =
        (import.meta.env.VITE_ACCOUNTS_WEB_URL as string | undefined) ??
        'http://localhost:5175/accounts';
      const target = `${accountsUrl}/login?redirect=${encodeURIComponent(window.location.href)}`;
      // accountsUrl comes from build-time config, so it is the allowlist
      // here; the check still stops a malformed value from turning into
      // a navigation to an unexpected origin or scheme.
      if (isSafeRedirect(target, window.location.origin, [accountsUrl])) {
        window.location.href = target;
      }
    }
  }, [status, isAuthenticated]);

  if (status === 'loading') return <PageSkeleton className={styles.boot} />;
  // We could not reach the auth service, which says nothing about whether
  // the session is still valid. Offer the retry instead of bouncing the
  // user to a login screen they may not even need.
  if (status === 'offline') return <SessionUnreachable onRetry={retry} />;
  if (!isAuthenticated) return null;

  return (
    <AppShell>
      <Outlet />
      <AiSuggestionsDock />
      <ShortcutsHelpDialog open={helpOpen} onClose={() => setHelpOpen(false)} />
      {(createdProjectId ?? defaultProjectId) && (
        <TaskCreateDialog
          projectId={(createdProjectId ?? defaultProjectId) as string}
          {...(defaultWorkspaceId ? { workspaceId: defaultWorkspaceId } : {})}
          open={taskDialogOpen}
          onClose={() => {
            setTaskDialogOpen(false);
            setCreatedProjectId(null);
          }}
        />
      )}
      {defaultProjectId && (
        <QuickCaptureDialog
          projectId={defaultProjectId}
          open={quickCaptureOpen}
          onClose={() => setQuickCaptureOpen(false)}
        />
      )}
      <NoProjectHintDialog
        open={noProjectHintOpen}
        onClose={() => setNoProjectHintOpen(false)}
        workspaceId={defaultWorkspaceId}
        canCreateProject={canCreateProject}
        onProjectCreated={(projectId) => {
          setNoProjectHintOpen(false);
          setCreatedProjectId(projectId);
          setTaskDialogOpen(true);
        }}
      />
    </AppShell>
  );
}

/**
 * Shown when the session probe could not reach the auth service on load.
 * Deliberately not a redirect: the session may well still be valid, and
 * a redirect would throw away whatever the user had on screen.
 */
function SessionUnreachable({ onRetry }: { onRetry: () => void }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <main aria-label={t('auth.offline.title')} className={styles.offline}>
      <h1 className={styles.offlineTitle}>{t('auth.offline.title')}</h1>
      <p className={styles.offlineText}>{t('auth.offline.description')}</p>
      <Button type="button" variant="primary" onClick={onRetry}>
        {t('auth.offline.retry')}
      </Button>
    </main>
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
