/**
 * Pathless layout route guarding all authenticated pages. Renders the
 * AppShell around its children. While bootstrap is in progress we render
 * nothing (Suspense fallback would also work). On unauthenticated state
 * the user is redirected to /login.
 */

import { isSafeRedirect } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { type ReactElement, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AppShell from '../components/layout/app-shell';
import { OPEN_CREATE_TASK_EVENT, OPEN_QUICK_CAPTURE_EVENT } from '../components/layout/glass-dock';
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
  const { t } = useTranslation('common');
  const { status } = useAuthBootstrap();
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
      <Dialog
        open={noProjectHintOpen}
        onClose={() => setNoProjectHintOpen(false)}
        title={t('tasks.new')}
      >
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '1rem',
            minInlineSize: '20rem',
          }}
        >
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
            {t('tasks.quick_create.no_projects.title')}
          </p>
          <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
            <Button type="button" variant="ghost" onClick={() => setNoProjectHintOpen(false)}>
              {t('tasks.form.cancel')}
            </Button>
            {defaultWorkspaceId ? (
              <Link
                to="/workspaces/$id/projects"
                params={{ id: defaultWorkspaceId }}
                onClick={() => setNoProjectHintOpen(false)}
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  paddingInline: '0.75rem',
                  paddingBlock: '0.375rem',
                  borderRadius: 'var(--nf-radius-md)',
                  background: 'var(--nf-color-accent)',
                  color: 'var(--nf-color-fg-on-accent)',
                  textDecoration: 'none',
                  fontSize: 'var(--nf-text-sm)',
                }}
              >
                {t('tasks.quick_create.no_projects.cta')}
              </Link>
            ) : null}
          </div>
        </div>
      </Dialog>
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
