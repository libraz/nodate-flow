/** Labelled hairline that separates one group of sign-in options from the next. */

import type { ReactElement, ReactNode } from 'react';

import styles from './or-divider.module.css';

export default function OrDivider({ children }: { children: ReactNode }): ReactElement {
  return (
    <div className={styles.divider}>
      <hr className={styles.rule} />
      <span>{children}</span>
      <hr className={styles.rule} />
    </div>
  );
}
