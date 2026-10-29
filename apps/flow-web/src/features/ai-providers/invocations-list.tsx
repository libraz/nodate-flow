/**
 * InvocationsList — workspace AI activity audit panel.
 *
 * Renders the most recent redacted LLM calls as a compact list. All
 * prompt / response bodies are already redacted at write time; this
 * component is a thin presentation layer.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import Badge from '@nodate-flow/ui/primitives/badge';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import type { CSSProperties, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { formatEpochDateTime } from '../../lib/format';
import ui from '../settings/settings-ui.module.css';
import { type AiInvocation, useAiInvocationsQuery } from './invocations-api';
import styles from './invocations-list.module.css';

function toneForStatus(status: string): 'accent' | 'warning' | 'danger' {
  if (status === 'ok') return 'accent';
  if (status === 'blocked') return 'warning';
  return 'danger';
}

function InvocationRow({
  row,
  style,
}: {
  row: AiInvocation;
  style: CSSProperties | undefined;
}): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const locale = i18n.resolvedLanguage ?? 'en';
  return (
    <li className={`${styles.row} ${motion.enter}`} style={style}>
      <div className={styles.headline}>
        <Badge tone={toneForStatus(row.status)}>{row.status}</Badge>
        <strong className={styles.purpose}>{row.purpose}</strong>
        <span className={styles.model}>{row.model}</span>
        <span className={styles.time}>{formatEpochDateTime(row.invokedAt, locale)}</span>
      </div>
      <p className={styles.prompt}>{row.promptRedacted}</p>
      {row.errorCode ? (
        <span className={styles.error}>{t('ai_activity.error_code', { code: row.errorCode })}</span>
      ) : null}
    </li>
  );
}

export default function InvocationsList({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const { data } = useAiInvocationsQuery(workspaceId);
  const staggerFor = useFirstLoadStagger();
  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('ai_activity.title')}</h1>
          <p className={ui.lede}>{t('ai_activity.description')}</p>
        </div>
      </header>
      {data.length === 0 ? (
        <EmptyState titleAs="p" title={t('ai_activity.empty')} className={ui.empty} />
      ) : (
        <ul className={styles.list}>
          {data.map((row, index) => (
            <InvocationRow key={row.id} row={row} style={staggerFor(index)} />
          ))}
        </ul>
      )}
    </div>
  );
}
