import SkipLink from '@nodate-flow/ui/a11y/skip-link';
import { type ReactElement, type ReactNode, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import ActiveTimeboxBar from '../../features/timeboxes/active-timebox-bar';
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
      {/*
       * Persistent active-timebox bar. Self-fetches the actor's
       * workspaces (suspense) and renders `null` whenever no
       * workspace has a running timebox, so the slot collapses to
       * zero height when idle.
       */}
      <div className={styles.activeTimeboxSlot}>
        <Suspense fallback={null}>
          <ActiveTimeboxBar />
        </Suspense>
      </div>
      <main id="main-content" tabIndex={-1} className={`${styles.main} nf-focus-ring-inset`}>
        {children}
      </main>
      <GlassDock />
    </div>
  );
}
