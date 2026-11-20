/**
 * AI consequence marker: one convention for whether an AI action changes data
 * now or only proposes something. A filled sparkle plus "Applies now" marks the
 * former; an outline sparkle plus "Suggestion" marks the latter.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import { Sparkles } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './ai-consequence.module.css';

export type AiConsequenceKind = 'applies' | 'suggests';

/** The sparkle alone, for use inside a button next to its label. */
export function AiSparkle({ kind }: { kind: AiConsequenceKind }): ReactElement {
  return (
    <Sparkles
      size={14}
      aria-hidden="true"
      className={styles.icon}
      fill={kind === 'applies' ? 'currentColor' : 'none'}
    />
  );
}

/** Sparkle plus "Applies now" / "Suggestion", for headings and status lines. */
export default function AiConsequence({
  kind,
  className,
}: {
  kind: AiConsequenceKind;
  className?: string | undefined;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  return (
    <span
      className={cx(styles.tag, kind === 'applies' ? styles.applies : styles.suggests, className)}
    >
      <AiSparkle kind={kind} />
      {kind === 'applies' ? t('consequence.applies_now') : t('consequence.suggestion')}
    </span>
  );
}
