/**
 * LinkedEventsEmpty — discreet empty state shown inside the section
 * body when no events are linked yet.
 *
 * Renders a hairline calendar-with-thread illustration, a one-line
 * heading, a one-line body, and a primary CTA that opens the picker.
 * The illustration is decorative; the heading is the announceable
 * label.
 *
 * Thin wrapper around the shared {@link EmptyState} primitive. The SVG
 * is forwarded via `icon`, the CTA via `action`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

export interface LinkedEventsEmptyProps {
  onTriggerClick: () => void;
}

export default function LinkedEventsEmpty({
  onTriggerClick,
}: LinkedEventsEmptyProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  return (
    <EmptyState
      icon={
        <svg
          viewBox="0 0 64 32"
          fill="none"
          stroke="currentColor"
          strokeWidth={1}
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          focusable="false"
          // nf-token-override: component dimension, not a spacing step
          style={{ inlineSize: '4rem', blockSize: '2rem' }}
        >
          {/* a sparse calendar grid on the inline-start */}
          <rect x="3" y="6" width="20" height="20" rx="2" />
          <path d="M3 12h20" />
          <path d="M9 6v-2" />
          <path d="M17 6v-2" />
          <circle cx="9" cy="18" r="1.25" />
          {/* thread weaving across to the right edge */}
          <path d="M23 16c6 0 8 -6 14 -6s8 12 14 12s8 -6 10 -6" />
        </svg>
      }
      title={t('empty.title')}
      description={t('empty.body')}
      action={
        <Button type="button" variant="default" size="sm" onClick={onTriggerClick}>
          {t('empty.cta')}
        </Button>
      }
    />
  );
}
