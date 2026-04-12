/**
 * Global keyboard shortcuts for the authenticated shell.
 *
 * Bindings are suppressed while the user is typing in an input, textarea,
 * or contenteditable element — or when a dialog (role="dialog") is open
 * — to avoid hijacking form interactions.
 *
 * Single-key shortcuts fire on keydown. Two-key "chord" shortcuts (e.g.
 * g → i) use a 500ms leader window: the first key arms the leader and
 * the second key must follow within the window.
 */

import { useNavigate } from '@tanstack/react-router';
import { useCallback, useEffect, useRef } from 'react';

/** Returns true when focus is on an interactive text field. */
function isTyping(e: KeyboardEvent): boolean {
  const el = e.target;
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (el.isContentEditable) return true;
  return false;
}

/** Returns true when a modal dialog is currently open and focused. */
function isDialogOpen(): boolean {
  const el = document.activeElement;
  if (!el) return false;
  return el.closest('[role="dialog"]') !== null;
}

export interface ShortcutBinding {
  /** Display label for the shortcut key(s). */
  keys: string;
  /** i18n key describing the action. */
  labelKey: string;
  /** Section grouping key for the help dialog. */
  sectionKey: string;
}

/** All registered shortcuts, exported for the help dialog. */
export const SHORTCUT_BINDINGS: ShortcutBinding[] = [
  { keys: 'c', labelKey: 'shortcuts.create_task', sectionKey: 'shortcuts.section_tasks' },
  { keys: '/', labelKey: 'shortcuts.focus_search', sectionKey: 'shortcuts.section_navigation' },
  { keys: 'g i', labelKey: 'shortcuts.go_inbox', sectionKey: 'shortcuts.section_navigation' },
  { keys: 'g t', labelKey: 'shortcuts.go_today', sectionKey: 'shortcuts.section_navigation' },
  { keys: 'g w', labelKey: 'shortcuts.go_workspaces', sectionKey: 'shortcuts.section_navigation' },
  { keys: 'g s', labelKey: 'shortcuts.go_settings', sectionKey: 'shortcuts.section_navigation' },
  { keys: '?', labelKey: 'shortcuts.show_help', sectionKey: 'shortcuts.section_general' },
];

export interface UseKeyboardShortcutsOptions {
  onCreateTask: () => void;
  onShowHelp: () => void;
}

export function useKeyboardShortcuts({
  onCreateTask,
  onShowHelp,
}: UseKeyboardShortcutsOptions): void {
  const navigate = useNavigate();
  const leaderRef = useRef<string | null>(null);
  const leaderTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clearLeader = useCallback(() => {
    leaderRef.current = null;
    if (leaderTimerRef.current) {
      clearTimeout(leaderTimerRef.current);
      leaderTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    const handler = (e: KeyboardEvent): void => {
      // Never intercept when user is typing or a modifier is held.
      if (isTyping(e)) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      // Never intercept when a dialog is open (except Escape which
      // is handled by the dialog itself).
      if (isDialogOpen()) return;

      const key = e.key.toLowerCase();

      // --- Chord handling (g → X) ---
      if (leaderRef.current === 'g') {
        clearLeader();
        switch (key) {
          case 'i':
            e.preventDefault();
            void navigate({ to: '/inbox' });
            return;
          case 't':
            e.preventDefault();
            void navigate({ to: '/today' });
            return;
          case 'w':
            e.preventDefault();
            void navigate({ to: '/workspaces' });
            return;
          case 's':
            e.preventDefault();
            void navigate({ to: '/settings/profile' });
            return;
        }
        return;
      }

      // --- Leader key ---
      if (key === 'g') {
        clearLeader();
        leaderRef.current = 'g';
        leaderTimerRef.current = setTimeout(clearLeader, 500);
        return;
      }

      // --- Single-key shortcuts ---
      switch (key) {
        case 'c':
          e.preventDefault();
          onCreateTask();
          return;
        case '/':
          e.preventDefault();
          // Focus the global search input in the top bar.
          {
            const searchBtn = document.querySelector<HTMLButtonElement>('[data-search-trigger]');
            searchBtn?.click();
          }
          return;
        case '?':
          e.preventDefault();
          onShowHelp();
          return;
      }
    };

    document.addEventListener('keydown', handler);
    return () => {
      document.removeEventListener('keydown', handler);
      clearLeader();
    };
  }, [navigate, onCreateTask, onShowHelp, clearLeader]);
}
