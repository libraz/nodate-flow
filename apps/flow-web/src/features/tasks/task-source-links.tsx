import Icon from '@nodate-flow/ui/icon';
import { ExternalLink } from 'lucide-react';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import { safeSourceUrl } from '../inbox/source-intake/safe-source-url';
import TaskSectionHeading from './task-section-heading';
import styles from './task-source-links.module.css';
import { useTaskSourcesQuery } from './task-source-links-api';

export interface TaskSourceLinksProps {
  taskId: string;
}

export default function TaskSourceLinks({ taskId }: TaskSourceLinksProps): ReactElement | null {
  const { t } = useTranslation('inbox');
  const headingId = useId();
  const sources = useTaskSourcesQuery(taskId);
  if (sources.length === 0) return null;

  return (
    <section className={styles.section} aria-labelledby={headingId}>
      <TaskSectionHeading id={headingId} title={t('sourceIntake.task_sources')} />
      <ul className={styles.list}>
        {sources.map((source) => {
          const sourceUrl = safeSourceUrl(source.sourceUrl);
          return (
            <li className={styles.source} key={source.intakeItemId}>
              <div className={styles.text}>
                <p className={styles.title}>{source.title}</p>
                {source.body ? <p className={styles.body}>{source.body}</p> : null}
              </div>
              {sourceUrl ? (
                <a className={styles.link} href={sourceUrl} target="_blank" rel="noreferrer">
                  <Icon icon={ExternalLink} decorative />
                  <span>{t('sourceIntake.open_source')}</span>
                </a>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
