import SkipLink from '@nodate-flow/ui/a11y/skip-link';
import { cx } from '@nodate-flow/ui/lib/cx';
import PageSkeleton from '@nodate-flow/ui/primitives/page-skeleton';
import { useRouterState } from '@tanstack/react-router';
import { type ReactElement, type ReactNode, Suspense, useState } from 'react';
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
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  // Alternate between two equivalent animation lists on each pathname change so
  // the enter animation replays without remounting the routed subtree.
  const [enter, setEnter] = useState({ pathname, alternate: false });
  if (enter.pathname !== pathname) setEnter({ pathname, alternate: !enter.alternate });
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
       * workspaces (suspense) and collapses its row to zero height
       * whenever no workspace has a running timebox.
       */}
      <div className={styles.activeTimeboxSlot}>
        <Suspense fallback={null}>
          <ActiveTimeboxBar />
        </Suspense>
      </div>
      <main
        id="main-content"
        tabIndex={-1}
        className={cx(
          styles.main,
          enter.alternate ? styles.mainEnterB : styles.mainEnterA,
          'nf-focus-ring-inset',
        )}
      >
        <Suspense fallback={<PageSkeleton />}>{children}</Suspense>
      </main>
      <GlassDock />
    </div>
  );
}
