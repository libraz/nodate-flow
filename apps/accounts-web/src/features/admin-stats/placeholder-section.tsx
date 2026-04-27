/**
 * PlaceholderSection — visual hint that more metrics are coming soon.
 *
 * Renders a low-contrast Card with a title + body so the dashboard
 * has a balanced footprint while we only ship two KPI tiles. The
 * section is purely decorative — it carries no actionable controls.
 */

import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';

export interface PlaceholderSectionProps {
  title: string;
  body: string;
}

/** Decorative "more metrics coming soon" panel. */
function PlaceholderSection({ title, body }: PlaceholderSectionProps): ReactElement {
  return (
    <Card
      aria-hidden="true"
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
        padding: 'var(--nf-space-6)',
        background: 'color-mix(in srgb, var(--nf-color-surface) 70%, transparent)',
        borderStyle: 'dashed',
      }}
    >
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-sm)',
          fontWeight: 600,
          color: 'var(--nf-color-fg-muted)',
        }}
      >
        {title}
      </p>
      <p
        style={{
          margin: 0,
          fontSize: 'var(--nf-text-sm)',
          color: 'var(--nf-color-fg-muted)',
          lineHeight: 1.5,
        }}
      >
        {body}
      </p>
    </Card>
  );
}

export default PlaceholderSection;
