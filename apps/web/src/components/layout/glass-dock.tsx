import Icon from '@nodate-flow/ui/icon';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { type LucideIcon, Plus, Sparkles, Zap } from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './glass-dock.module.css';

interface DockAction {
  key: 'new_task' | 'quick_capture' | 'ai_assist';
  icon: LucideIcon;
}

const ACTIONS: readonly DockAction[] = [
  { key: 'new_task', icon: Plus },
  { key: 'quick_capture', icon: Zap },
  { key: 'ai_assist', icon: Sparkles },
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
            setPaletteOpen(true);
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
      <Dialog open={paletteOpen} onClose={handleClose} title={t('dock.command_palette.title')}>
        <p>{t('dock.command_palette.coming_soon')}</p>
      </Dialog>
    </>
  );
}
