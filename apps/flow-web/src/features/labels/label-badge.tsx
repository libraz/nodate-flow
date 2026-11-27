/**
 * LabelBadge — renders a single label as a colored chip/badge.
 */

import { X } from 'lucide-react';
import type { CSSProperties, ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import styles from './label-badge.module.css';

export interface LabelBadgeProps {
  name: string;
  color: string;
  onRemove?: () => void;
}

export default function LabelBadge({ name, color, onRemove }: LabelBadgeProps): ReactElement {
  const { t } = useTranslation('labels');

  return (
    <span className={styles.badge} style={{ '--label-color': color } as CSSProperties}>
      <span className={styles.dot} aria-hidden="true" />
      <span className={styles.name} title={name}>
        {name}
      </span>
      {onRemove ? (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className={styles.remove}
          aria-label={t('badge.remove', { name })}
        >
          <X size={12} aria-hidden />
        </button>
      ) : null}
    </span>
  );
}
