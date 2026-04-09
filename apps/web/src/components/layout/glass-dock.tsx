import Icon from '@nodate-flow/ui/icon';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { useNavigate } from '@tanstack/react-router';
import { type LucideIcon, Plus, Sparkles, Zap } from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import CommandPalette from './command-palette';
import styles from './glass-dock.module.css';

type DockActionKey = 'new_task' | 'quick_capture' | 'ai_assist';
type DockActionTarget = { kind: 'navigate'; href: string } | { kind: 'palette' };

interface DockAction {
  key: DockActionKey;
  icon: LucideIcon;
  target: DockActionTarget;
}

// Dock actions. `new_task` and `ai_assist` don't have dedicated dialogs
// yet — they open the command palette so the user can jump directly to
// a project / task / workspace instead of being bounced to a random
// landing page. `quick_capture` still deep-links to the inbox, which is
// where the capture surface lives.
const ACTIONS: readonly DockAction[] = [
  { key: 'new_task', icon: Plus, target: { kind: 'navigate', href: '/inbox' } },
  { key: 'quick_capture', icon: Zap, target: { kind: 'navigate', href: '/inbox' } },
  { key: 'ai_assist', icon: Sparkles, target: { kind: 'palette' } },
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

  useEffect(() => {
    const handler = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((prev) => !prev);
      }
    };
    window.addEventListener('keydown', handler);
    return () => {
      window.removeEventListener('keydown', handler);
    };
  }, []);

  const handleClose = (): void => {
    setPaletteOpen(false);
  };

  return (
    <>
      <div className={styles.dock} role="toolbar" aria-label={t('dock.quick_actions')}>
        {ACTIONS.map((action) => {
          const label = t(actionLabelKey(action.key));
          const handleClick = (): void => {
            if (action.target.kind === 'palette') {
              setPaletteOpen(true);
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
      <CommandPalette open={paletteOpen} onClose={handleClose} />
    </>
  );
}
