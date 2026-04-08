import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { useNavigate } from '@tanstack/react-router';
import { LogOut, Moon, Search, Sun } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AiCostMeter from '../../features/ai-providers/cost-meter';
import { authStore } from '../../features/auth/auth-store';
import { type SupportedLanguage, setLanguage } from '../../i18n';
import { apiBaseUrl } from '../../lib/sdk';
import { type ConcreteTheme, concreteThemes, useTheme } from '../../providers/theme-provider';
import styles from './top-bar.module.css';

function nextTheme(current: ConcreteTheme): ConcreteTheme {
  const idx = concreteThemes.indexOf(current);
  const next = concreteThemes[(idx + 1) % concreteThemes.length];
  return next ?? 'aurora-light';
}

export default function TopBar(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { resolved, setPreference } = useTheme();
  const navigate = useNavigate();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const isDark = resolved === 'aurora-dark' || resolved === 'dotline-dark';

  const handleThemeToggle = (): void => {
    setPreference(nextTheme(resolved));
  };

  const handleLanguageToggle = (): void => {
    const current = (i18n.resolvedLanguage ?? 'en') as SupportedLanguage;
    setLanguage(current === 'en' ? 'ja' : 'en');
  };

  const handleLogout = async (): Promise<void> => {
    try {
      await fetch(`${apiBaseUrl}/auth/logout`, {
        method: 'POST',
        credentials: 'include',
      });
    } catch {
      // Even on network failure, clear local state and bounce to /login.
    }
    authStore.getState().clearSession();
    void navigate({ to: '/login', replace: true });
  };

  const currentLang = (i18n.resolvedLanguage ?? 'en').toUpperCase();

  return (
    <>
      <header className={styles.topBar}>
        <div className={styles.left}>nodate-flow</div>
        <div className={styles.center}>
          <div className={styles.search}>
            <button
              type="button"
              className={styles.searchButton}
              aria-label={t('topbar.search.placeholder')}
              onClick={() => {
                setPaletteOpen(true);
              }}
            >
              <Icon icon={Search} decorative />
              <span>{t('topbar.search.placeholder')}</span>
              <span className={styles.searchButtonShortcut} aria-hidden="true">
                Cmd+K
              </span>
            </button>
          </div>
        </div>
        <div className={styles.right}>
          <AiCostMeter />
          <button
            type="button"
            className={styles.iconButton}
            onClick={handleThemeToggle}
            aria-label={t('topbar.theme.toggle')}
          >
            <Icon icon={isDark ? Moon : Sun} decorative />
          </button>
          <button
            type="button"
            className={cx(styles.iconButton, styles.langToggle)}
            onClick={handleLanguageToggle}
            aria-label={t('topbar.language.toggle')}
          >
            {currentLang}
          </button>
          <button
            type="button"
            className={styles.iconButton}
            onClick={() => {
              void handleLogout();
            }}
            aria-label={t('auth.logout')}
          >
            <Icon icon={LogOut} decorative />
          </button>
          <div className={styles.avatar} aria-hidden="true">
            NF
          </div>
        </div>
      </header>
      <Dialog
        open={paletteOpen}
        onClose={() => {
          setPaletteOpen(false);
        }}
        title={t('dock.command_palette.title')}
      >
        <p>{t('dock.command_palette.coming_soon')}</p>
      </Dialog>
    </>
  );
}
