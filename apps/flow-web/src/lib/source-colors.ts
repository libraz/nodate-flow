/**
 * source-colors — single source of truth for hex literals that *cannot*
 * flow through the design-token pipeline.
 *
 * These values are exempt from the `--nf-color-*` rule for two reasons:
 *
 *   1. They are persisted by the API and surfaced verbatim on rendering
 *      surfaces that may live outside a themed canvas (e.g. event chips
 *      in calendar exports, public-share pages, or printed views). The
 *      hex is the canonical cross-theme identity, not a swatch tied to
 *      the active theme.
 *   2. They reproduce external brand identities (GitHub, Slack, Google,
 *      etc.). The brands ship a fixed hex and we are not allowed to
 *      retint them per active theme.
 *
 * nf-color-override-file: every literal in this module is one of the two
 * cases above. The whole-file form is deliberate — the line-scoped marker
 * would have to be repeated on each of the nineteen entries, and a
 * per-entry exemption would imply the entries were individually
 * considered when what is exempt is the module's whole reason to exist.
 */

/**
 * Curated 10-swatch calendar palette. Each swatch carries a stable hex
 * (the value persisted by the API and rendered on event chips outside
 * the themed canvas) and a paired design token used to paint the picker
 * swatch. Swatch rendering goes through the token so the picker reads
 * as native to the active theme; the persisted hex is retained as the
 * canonical cross-theme identity. Custom hex pickers are out of scope
 * for v1.
 */
export const CALENDAR_EVENT_PALETTE: ReadonlyArray<{
  hex: string;
  token: string;
  nameKey: string;
}> = [
  {
    hex: '#2563eb',
    token: 'var(--nf-cal-color-1)',
    nameKey: 'calendar.settings.general.color.blue',
  },
  {
    hex: '#0891b2',
    token: 'var(--nf-cal-color-2)',
    nameKey: 'calendar.settings.general.color.cyan',
  },
  {
    hex: '#16a34a',
    token: 'var(--nf-cal-color-3)',
    nameKey: 'calendar.settings.general.color.green',
  },
  {
    hex: '#ca8a04',
    token: 'var(--nf-cal-color-4)',
    nameKey: 'calendar.settings.general.color.amber',
  },
  {
    hex: '#ea580c',
    token: 'var(--nf-cal-color-5)',
    nameKey: 'calendar.settings.general.color.orange',
  },
  {
    hex: '#dc2626',
    token: 'var(--nf-cal-color-6)',
    nameKey: 'calendar.settings.general.color.red',
  },
  {
    hex: '#db2777',
    token: 'var(--nf-cal-color-7)',
    nameKey: 'calendar.settings.general.color.pink',
  },
  {
    hex: '#9333ea',
    token: 'var(--nf-cal-color-8)',
    nameKey: 'calendar.settings.general.color.purple',
  },
  {
    hex: '#475569',
    token: 'var(--nf-cal-color-9)',
    nameKey: 'calendar.settings.general.color.slate',
  },
  {
    hex: '#0f172a',
    token: 'var(--nf-cal-color-10)',
    nameKey: 'calendar.settings.general.color.ink',
  },
];

/**
 * Brand colors for external integration sources used in the constraint
 * state graph (and any other surface that visualises which feed drove
 * a transition). The state graph paints inbound dashed edges with these
 * hexes so GitHub / Slack / Google nodes stay recognisable across all
 * themes.
 */
export const CONSTRAINT_STATE_COLORS = {
  github: '#6e5494',
  slack: '#4a154b',
  google: '#4285f4',
} as const;

/**
 * Text-strength colour tokens for timeline event source tags, one per lane.
 * The tag paints its label in this colour over a light wash of the same, so
 * each value must be an `-fg` token that holds 4.5:1 on that wash.
 */
export const INTEGRATION_SOURCE_COLORS = {
  github: 'var(--nf-color-state-review-fg)',
  slack: 'var(--nf-color-accent-fg)',
  google: 'var(--nf-color-info-fg)',
  signal: 'var(--nf-color-info-fg)',
  ai: 'var(--nf-color-success-fg)',
  task: 'var(--nf-color-warning-fg)',
} as const;

/**
 * Label swatch grid (light / standard / deep rows of ten). The values
 * match the sibling calendar product so a colour picked in either lands
 * on the same swatch.
 */
export const LABEL_SWATCH_ROWS: ReadonlyArray<ReadonlyArray<string>> = [
  [
    '#F59A9A',
    '#FFAA8C',
    '#F9CB7A',
    '#FEE08A',
    '#8EE3BC',
    '#8ADAD6',
    '#9BD3FB',
    '#D6C2EE',
    '#F9A9C1',
    '#C9C9CE',
  ],
  [
    '#E73B3B',
    '#FF7043',
    '#F5A623',
    '#FDC02D',
    '#2ECC87',
    '#26B5B0',
    '#47B2F7',
    '#B38BDC',
    '#F35F8C',
    '#8F8F8F',
  ],
  [
    '#B3261E',
    '#D5501F',
    '#C47A06',
    '#C99A00',
    '#18935E',
    '#157F7B',
    '#2273C9',
    '#7C55B5',
    '#C23A66',
    '#55555A',
  ],
];

/** Ten colours a calendar member can pick for their own events. */
export const MEMBER_COLOR_PALETTE: ReadonlyArray<string> = [
  '#47B2F7',
  '#F35F8C',
  '#B38BDC',
  '#FDC02D',
  '#E73B3B',
  '#2ECC87',
  '#F5A623',
  '#8F8F8F',
  '#42A5F5',
  '#FF7043',
];

/** Colour used when an event or member carries no valid hex. */
export const FALLBACK_EVENT_COLOR = '#47B2F7';

/** True for a `#RRGGBB` string (either case). */
export function isHexColor(value: string): boolean {
  if (value.length !== 7 || value[0] !== '#') return false;
  for (let i = 1; i < 7; i += 1) {
    const c = value.charCodeAt(i);
    const digit = c >= 48 && c <= 57;
    const upper = c >= 65 && c <= 70;
    const lower = c >= 97 && c <= 102;
    if (!digit && !upper && !lower) return false;
  }
  return true;
}
