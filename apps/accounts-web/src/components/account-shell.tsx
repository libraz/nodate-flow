/**
 * Frame for every signed-in page: a navigation sidebar on the app canvas
 * and a left-aligned content column. Below 768px the sidebar folds into a
 * compact top bar with the navigation as a horizontal tab row.
 */

import SkipLink from '@nodate-flow/ui/a11y/skip-link';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import { Link } from '@tanstack/react-router';
import type { ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import type { AuthUser } from '../features/auth/auth-store';
import { flowWebUrl } from '../lib/flow-web-url';

import {
  AdminIcon,
  BackIcon,
  ProfileIcon,
  SecurityIcon,
  WorkspacesIcon,
} from './account-nav-icons';
import styles from './account-shell.module.css';

const MAIN_ID = 'account-main';

export interface AccountShellProps {
  user: AuthUser;
  /** Lifts the reading measure for data-heavy pages (instance admin tables). */
  wide?: boolean;
  children: ReactNode;
}

function initialsOf(user: AuthUser): string {
  const source = user.displayName.trim() || user.email;
  return Array.from(source)[0]?.toUpperCase() ?? '';
}

export default function AccountShell({ user, wide, children }: AccountShellProps): ReactElement {
  const { t } = useTranslation('auth');
  const { t: tAdmin } = useTranslation('admin');
  const itemProps = {
    className: `${styles.item} nf-focus-ring-inset`,
    activeProps: { className: styles.itemActive },
  };

  return (
    <div className={styles.shell}>
      <SkipLink targetId={MAIN_ID}>{t('account.skip_to_content')}</SkipLink>
      <aside className={styles.sidebar}>
        <div className={styles.brand}>nodate-flow</div>

        <nav className={styles.nav} aria-label={t('account.nav_label')}>
          <Link to="/profile" {...itemProps}>
            <ProfileIcon />
            <span className={styles.label}>{t('account.nav.profile')}</span>
          </Link>
          <Link to="/security" {...itemProps}>
            <SecurityIcon />
            <span className={styles.label}>{t('account.nav.security')}</span>
          </Link>
          <Link to="/workspaces" {...itemProps}>
            <WorkspacesIcon />
            <span className={styles.label}>{t('account.nav.workspaces')}</span>
          </Link>
          {user.isInstanceAdmin ? (
            <Link to="/admin" {...itemProps}>
              <AdminIcon />
              <span className={styles.label}>{tAdmin('title')}</span>
            </Link>
          ) : null}
        </nav>

        <a href={flowWebUrl('/')} className={`${styles.back} nf-focus-ring-inset`}>
          <BackIcon />
          <span className={styles.backLabel}>{t('account.back_to_flow')}</span>
        </a>

        <div className={styles.user}>
          <Avatar
            size="sm"
            alt={user.displayName || user.email}
            initials={initialsOf(user)}
            {...(user.avatarUrl ? { src: user.avatarUrl } : {})}
          />
          <div className={styles.userText}>
            <span className={styles.userName}>{user.displayName || user.email}</span>
            {user.displayName ? <span className={styles.userEmail}>{user.email}</span> : null}
          </div>
        </div>
      </aside>

      <main id={MAIN_ID} tabIndex={-1} className={styles.main}>
        <div className={wide ? `${styles.content} ${styles.contentWide}` : styles.content}>
          {children}
        </div>
      </main>
    </div>
  );
}
