/**
 * Keyboard shortcuts, the shortcuts-help dialog and the task-creation
 * dialogs shared by every authenticated page, including the 404 page.
 */

import { type ReactElement, useCallback, useEffect, useState } from 'react';

import NoProjectHintDialog from '../../features/tasks/no-project-hint-dialog';
import QuickCaptureDialog from '../../features/tasks/quick-capture-dialog';
import TaskCreateDialog from '../../features/tasks/task-create-dialog';
import { isWorkspaceAdmin, useCurrentWorkspaceRole } from '../../lib/use-current-workspace-role';
import { useDefaultProjectId } from '../../lib/use-default-project';
import { useKeyboardShortcuts } from '../../lib/use-keyboard-shortcuts';
import { OPEN_SHORTCUTS_HELP_EVENT } from '../page-help';
import ShortcutsHelpDialog from '../shortcuts-help-dialog';
import { OPEN_CREATE_TASK_EVENT, OPEN_QUICK_CAPTURE_EVENT } from './glass-dock';

export default function ShellOverlays(): ReactElement {
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

  // Bridge FAB button clicks (window CustomEvents) to the dialog open
  // state. Keeping a single dialog instance here means the keyboard
  // shortcut and the FAB share one mounted dialog tree.
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

  return (
    <>
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
    </>
  );
}
