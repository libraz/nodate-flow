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
 * Brand and category colors for timeline event source tags. External
 * sources reuse the upstream brand hex; internal categories (signal /
 * ai / task) use a short curated palette so each lane in the timeline
 * stays distinguishable at a glance.
 */
export const INTEGRATION_SOURCE_COLORS = {
  github: '#6e5494',
  slack: '#4a154b',
  google: '#4285f4',
  signal: '#0ea5e9',
  ai: '#10b981',
  task: '#f59e0b',
} as const;
