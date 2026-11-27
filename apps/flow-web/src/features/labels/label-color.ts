/**
 * Default colour for a label created from a name.
 */

import { LABEL_SWATCH_ROWS } from '../../lib/source-colors';

const PALETTE = LABEL_SWATCH_ROWS[1] ?? [];

/** Stable swatch for a new label: the same name always gets the same colour. */
export function pickLabelColor(name: string): string {
  let hash = 0;
  for (const ch of name) hash = (hash * 31 + (ch.codePointAt(0) ?? 0)) >>> 0;
  // nf-color-override: label colours are hex values the API persists, so the fallback is one too
  return PALETTE[hash % PALETTE.length] ?? '#8F8F8F';
}
