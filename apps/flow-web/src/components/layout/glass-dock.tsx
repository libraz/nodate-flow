import Icon from '@nodate-flow/ui/icon';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { useNavigate } from '@tanstack/react-router';
import { type LucideIcon, Plus, Sparkles, Zap } from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import CommandPalette from './command-palette';
import styles from './glass-dock.module.css';

type DockActionKey = 'new_task' | 'quick_capture' | 'ai_assist';
type DockActionTarget =
  | { kind: 'navigate'; href: string }
  | { kind: 'palette' }
  | { kind: 'palette_command' }
  | { kind: 'event'; name: string };

interface DockAction {
  key: DockActionKey;
  icon: LucideIcon;
  target: DockActionTarget;
}

/** Event name other components can dispatch to open the palette. */
export const OPEN_COMMAND_PALETTE_EVENT = 'nf:open-command-palette';

/**
 * Event name dispatched by the `new_task` dock button. The authenticated
 * layout listens for this and opens the shared TaskCreateDialog with the
 * resolved default project.
 */
export const OPEN_CREATE_TASK_EVENT = 'nf:open-create-task';

/**
 * Event name dispatched by the `quick_capture` dock button. The
 * authenticated layout listens for this and opens the QuickCaptureDialog.
 */
export const OPEN_QUICK_CAPTURE_EVENT = 'nf:open-quick-capture';

// Each dock button has a distinct behavior:
// - `new_task` (Plus): opens TaskCreateDialog directly for a smart default project
// - `quick_capture` (Zap): opens the lightweight QuickCaptureDialog
// - `ai_assist` (Sparkles): opens the palette in NL command mode ("> ")
const ACTIONS: readonly DockAction[] = [
  { key: 'new_task', icon: Plus, target: { kind: 'event', name: OPEN_CREATE_TASK_EVENT } },
  {
    key: 'quick_capture',
    icon: Zap,
    target: { kind: 'event', name: OPEN_QUICK_CAPTURE_EVENT },
  },
  { key: 'ai_assist', icon: Sparkles, target: { kind: 'palette_command' } },
];

function actionLabelKey(
  key: DockAction['key'],
): 'dock.new_task' | 'dock.quick_capture' | 'dock.ai_assist' {
  switch (key) {
    case 'new_task':
      return 'dock.new_task';
    case 'quick_capture':
      return 'dock.quick_capture';
    case 'ai_assist':
      return 'dock.ai_assist';
  }
}

export default function GlassDock(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteCommandMode, setPaletteCommandMode] = useState(false);

  useEffect(() => {
    const handler = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((prev) => !prev);
      }
    };
    const openHandler = (): void => {
      setPaletteOpen(true);
    };
    window.addEventListener('keydown', handler);
    window.addEventListener(OPEN_COMMAND_PALETTE_EVENT, openHandler);
    return () => {
      window.removeEventListener('keydown', handler);
      window.removeEventListener(OPEN_COMMAND_PALETTE_EVENT, openHandler);
    };
  }, []);

  const handleClose = (): void => {
    setPaletteOpen(false);
    setPaletteCommandMode(false);
  };

  return (
    <>
      <div className={styles.dock} role="toolbar" aria-label={t('dock.quick_actions')}>
        {ACTIONS.map((action) => {
          const label = t(actionLabelKey(action.key));
          const handleClick = (): void => {
            if (action.target.kind === 'palette_command') {
              setPaletteCommandMode(true);
              setPaletteOpen(true);
              return;
            }
            if (action.target.kind === 'palette') {
              setPaletteCommandMode(false);
              setPaletteOpen(true);
              return;
            }
            if (action.target.kind === 'event') {
              window.dispatchEvent(new CustomEvent(action.target.name));
              return;
            }
            void navigate({ to: action.target.href });
          };
          return (
            <Tooltip key={action.key} content={label}>
              <button
                type="button"
                className={styles.button}
                aria-label={label}
                onClick={handleClick}
              >
                <Icon icon={action.icon} decorative />
              </button>
            </Tooltip>
          );
        })}
        <span className={styles.divider} aria-hidden="true" />
        <span className={styles.shortcut}>Cmd+K</span>
      </div>
      <CommandPalette
        open={paletteOpen}
        onClose={handleClose}
        initialCommandMode={paletteCommandMode}
      />
    </>
  );
}
