/**
 * A member's display name, with their address beneath it when the API
 * returned one (only the workspace owner, or the member themself, gets it).
 */

import type { ReactElement } from 'react';

import styles from './member-identity.module.css';

export interface MemberIdentityProps {
  displayName: string;
  email?: string | undefined;
}

export default function MemberIdentity({ displayName, email }: MemberIdentityProps): ReactElement {
  return (
    <span className={styles.identity}>
      <span className={styles.name} title={displayName}>
        {displayName}
      </span>
      {email ? (
        <span className={styles.email} title={email}>
          {email}
        </span>
      ) : null}
    </span>
  );
}
