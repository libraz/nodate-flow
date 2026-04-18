import SkipLink from '@nodate-flow/ui/a11y/skip-link';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './app-shell.module.css';
import GlassDock from './glass-dock';
import Sidebar from './sidebar';
import TopBar from './top-bar';

export interface AppShellProps {
  children: ReactNode;
}

export default function AppShell({ children }: AppShellProps): ReactElement {
  const { t } = useTranslation('common');
  return (
    <div className={styles.shell}>
      <SkipLink targetId="main-content">{t('a11y.skipToContent')}</SkipLink>
      <div className={styles.sidebarSlot}>
        <Sidebar />
      </div>
      <div className={styles.topBarSlot}>
        <TopBar />
      </div>
      <main id="main-content" tabIndex={-1} className={styles.main}>
        {children}
      </main>
      <GlassDock />
    </div>
  );
}
