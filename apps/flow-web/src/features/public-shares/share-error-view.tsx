/**
 * ShareErrorView — branded invalid / expired / network fallback for the
 * public calendar share. Shared by the `/share/cal/$token` page (inside
 * `PublicPageLayout`) and, in a compact form, by the chromeless
 * `/embed/cal/$token` route. Anonymous visitors land here instead of the
 * root fatal screen.
 */

import { Calendar as CalendarIcon } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError, isNetworkError } from '../../lib/api-error';

export interface ShareErrorViewProps {
  error: unknown;
  /** Compact mode for the chromeless embed (no back link, tighter). */
  compact?: boolean;
}

/** Resolve the title/body i18n keys for the given share error. */
export function resolveShareErrorKeys(error: unknown): { titleKey: string; bodyKey: string } {
  const network = isNetworkError(error);
  const isExpired = error instanceof ApiError && error.code === 'SHARE.SHARE.EXPIRED';
  return {
    titleKey: network
      ? 'common.network_error'
      : isExpired
        ? 'share.error.title_expired'
        : 'share.error.title_invalid',
    bodyKey: network ? 'share.error.body_network' : 'share.error.body',
  };
}

export default function ShareErrorView({
  error,
  compact = false,
}: ShareErrorViewProps): ReactElement {
  const { t } = useTranslation();
  const { titleKey, bodyKey } = resolveShareErrorKeys(error);

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 'var(--nf-space-3)',
        textAlign: 'center',
        padding: compact ? 'var(--nf-space-6) var(--nf-space-4)' : 0,
      }}
    >
      <CalendarIcon
        size={compact ? 36 : 48}
        style={{ color: 'var(--nf-color-fg-subtle)' }}
        aria-hidden="true"
      />
      <h1
        style={{
          fontSize: compact ? 'var(--nf-text-lg)' : 'var(--nf-text-xl)',
          fontWeight: 'var(--nf-weight-semibold)',
          color: 'var(--nf-color-fg)',
          margin: 0,
        }}
      >
        {t(titleKey)}
      </h1>
      <p
        style={{
          color: 'var(--nf-color-fg-muted)',
          margin: 0,
          maxInlineSize: 'var(--nf-measure-narrow)',
        }}
      >
        {t(bodyKey)}
      </p>
      {!compact ? (
        <a
          href="/"
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            marginBlockStart: 'var(--nf-space-2)',
            paddingInline: 'var(--nf-space-4)',
            paddingBlock: 'var(--nf-space-2)',
            borderRadius: 'var(--nf-radius-md)',
            backgroundColor: 'var(--nf-color-accent)',
            color: 'var(--nf-color-fg-on-accent)',
            textDecoration: 'none',
            fontWeight: 'var(--nf-weight-medium)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('share.error.back')}
        </a>
      ) : null}
    </div>
  );
}
