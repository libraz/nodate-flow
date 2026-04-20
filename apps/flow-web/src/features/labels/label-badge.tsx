/**
 * LabelBadge — renders a single label as a colored chip/badge.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface LabelBadgeProps {
  name: string;
  color: string;
  onRemove?: () => void;
}

export default function LabelBadge({ name, color, onRemove }: LabelBadgeProps): ReactElement {
  const { t } = useTranslation('labels');

  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium leading-tight"
      style={{
        backgroundColor: `${color}20`,
        color,
        border: `1px solid ${color}40`,
      }}
    >
      <span
        className="inline-block h-2 w-2 rounded-full"
        style={{ backgroundColor: color }}
        aria-hidden="true"
      />
      {name}
      {onRemove ? (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="ms-0.5 rounded-full p-0.5 hover:bg-[var(--nf-color-bg-hover)] focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--nf-color-focus-ring)]"
          aria-label={t('badge.remove', { name })}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 16 16"
            fill="currentColor"
            className="h-3 w-3"
            aria-hidden="true"
          >
            <path d="M5.28 4.22a.75.75 0 0 0-1.06 1.06L6.94 8l-2.72 2.72a.75.75 0 1 0 1.06 1.06L8 9.06l2.72 2.72a.75.75 0 1 0 1.06-1.06L9.06 8l2.72-2.72a.75.75 0 0 0-1.06-1.06L8 6.94 5.28 4.22Z" />
          </svg>
        </button>
      ) : null}
    </span>
  );
}
