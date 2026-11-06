/** Title block that opens each signed-in account page. */

import type { ReactElement } from 'react';

import styles from './account-page.module.css';

export interface PageHeaderProps {
  title: string;
  description?: string;
}

export default function PageHeader({ title, description }: PageHeaderProps): ReactElement {
  return (
    <header className={styles.header}>
      <h1 className={styles.title}>{title}</h1>
      {description ? <p className={styles.description}>{description}</p> : null}
    </header>
  );
}
