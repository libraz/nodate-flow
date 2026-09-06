/**
 * The accent colour a calendar item's kind is drawn in.
 *
 * Shared rather than repeated per surface: the marker is how a reader
 * ties a row in the day sheet back to the chip they tapped in the month
 * grid, so the two have to resolve the same token for the same kind.
 */

/** Per-kind accent token; anything unrecognised falls back to the event colour. */
export function markerColorForKind(kind: string): string {
  switch (kind) {
    case 'block':
      return 'var(--nf-cal-block-color)';
    case 'free':
      return 'var(--nf-cal-free-color)';
    case 'milestone':
      return 'var(--nf-cal-milestone-color)';
    default:
      return 'var(--nf-cal-event-color)';
  }
}
