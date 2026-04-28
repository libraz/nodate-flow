/**
 * PublicPageLayout — shared chrome for unauthenticated standalone pages
 * (calendar shares, RSVP accept, lens shares, …). Renders an optional
 * brand header, a width-constrained `<main>` landmark, and an optional
 * footer. Each public page picks its own measure size and decides
 * whether to render the brand row.
 *
 * The component intentionally avoids reading auth state or routing
 * context so it can be reused from both authenticated test harnesses
 * and the production public routes without smuggling globals.
 */

import { Calendar as CalendarIcon } from 'lucide-react';
import type { CSSProperties, ReactElement, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

/** Width preset for the main column. Maps to the global measure tokens. */
export type PublicPageMeasure = 'content' | 'narrow';

export interface PublicPageLayoutProps {
  /**
   * Children rendered inside the `<main>` landmark.
   */
  children: ReactNode;
  /**
   * Accessible name for the `<main>` landmark. Defaults to the brand
   * label when omitted.
   */
  mainLabel?: string;
  /**
   * Maximum inline size of the main column. `content` (~48rem) is the
   * default; `narrow` (~32rem) suits single-card flows like RSVP accept.
   */
  measure?: PublicPageMeasure;
  /**
   * When true, render the small brand header above the main column.
   * The brand row already includes a "back to nodate-flow" link.
   */
  showBrandHeader?: boolean;
  /**
   * URL of an optional cover image rendered as a full-bleed banner
   * above the brand header. Used by calendar shares.
   */
  coverImageUrl?: string;
  /**
   * Additional content rendered between the brand header and main.
   * Used by calendar shares for the title / icon block.
   */
  beforeMain?: ReactNode;
  /** Optional footer rendered below `<main>`. */
  footer?: ReactNode;
  /**
   * Marks the layout as loading; forwards `aria-busy` to the `<main>`
   * landmark so assistive tech can announce skeleton state.
   */
  busy?: boolean;
  /**
   * Vertical alignment of `<main>` within the viewport. `center` flexes
   * the page so a single short card sits in the middle (RSVP accept);
   * `flow` (default) lets content stack naturally from the top.
   */
  alignMain?: 'flow' | 'center';
}

const measureToken: Record<PublicPageMeasure, string> = {
  content: 'var(--nf-measure-content)',
  narrow: 'var(--nf-measure-narrow)',
};

/**
 * Renders the shared public-page chrome. See {@link PublicPageLayoutProps}
 * for opt-in regions; with no props beyond `children` the component just
 * provides a width-constrained `<main>`.
 */
export default function PublicPageLayout({
  children,
  mainLabel,
  measure = 'content',
  showBrandHeader = false,
  coverImageUrl,
  beforeMain,
  footer,
  busy = false,
  alignMain = 'flow',
}: PublicPageLayoutProps): ReactElement {
  const { t } = useTranslation();
  const labelledBy = mainLabel ?? t('share.brand');
  const maxInlineSize = measureToken[measure];

  const rootStyle: CSSProperties = {
    minBlockSize: '100vh',
    backgroundColor: 'var(--nf-color-bg)',
    display: 'flex',
    flexDirection: 'column',
  };

  const mainStyle: CSSProperties =
    alignMain === 'center'
      ? {
          maxInlineSize,
          inlineSize: '100%',
          marginInline: 'auto',
          padding: 'var(--nf-space-6) var(--nf-space-4)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-4)',
          flex: 1,
          justifyContent: 'center',
        }
      : {
          maxInlineSize,
          marginInline: 'auto',
          padding: 'var(--nf-space-6) var(--nf-space-4)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
          inlineSize: '100%',
        };

  return (
    <div style={rootStyle}>
      {coverImageUrl ? (
        <div
          style={{
            blockSize: '12rem',
            backgroundImage: `url(${encodeURI(coverImageUrl)})`,
            backgroundSize: 'cover',
            backgroundPosition: 'center',
            backgroundColor: 'var(--nf-color-bg-subtle)',
          }}
          aria-hidden="true"
        />
      ) : null}

      {showBrandHeader ? (
        <header
          style={{
            maxInlineSize,
            inlineSize: '100%',
            marginInline: 'auto',
            padding: 'var(--nf-space-4)',
            display: 'flex',
            alignItems: 'center',
          }}
        >
          <a
            href="/"
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              color: 'var(--nf-color-fg)',
              fontWeight: 'var(--nf-weight-semibold)',
              fontSize: 'var(--nf-text-base)',
              textDecoration: 'none',
            }}
          >
            <CalendarIcon
              size={20}
              style={{ color: 'var(--nf-color-accent)' }}
              aria-hidden="true"
            />
            {t('share.brand')}
          </a>
        </header>
      ) : null}

      {beforeMain}

      <main aria-label={labelledBy} {...(busy ? { 'aria-busy': true } : {})} style={mainStyle}>
        {children}
      </main>

      {footer ? <footer>{footer}</footer> : null}
    </div>
  );
}
