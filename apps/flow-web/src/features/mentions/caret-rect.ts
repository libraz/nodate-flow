/**
 * Where a character sits on screen inside a `<textarea>`.
 *
 * A textarea exposes no geometry for its own text, so the position is
 * measured by laying the same text out again in a hidden element that
 * copies the textarea's typography and box, and reading back the offset
 * of a marker placed at the index we care about. This is the only way to
 * anchor a popup at the caret rather than under the whole field.
 *
 * The mirror is created and removed inside a single call: nothing is
 * cached, so a font swap, a resize, or a theme change cannot leave a
 * stale layout behind, and no element outlives the measurement.
 */

/** Viewport-space geometry of one character, in physical CSS pixels. */
export interface CaretRect {
  /** Distance from the viewport's left edge. */
  left: number;
  /** Distance from the viewport's top edge to the top of the line. */
  top: number;
  /** Distance from the viewport's top edge to the bottom of the line. */
  bottom: number;
}

/**
 * Properties that decide where a glyph lands. Everything else about the
 * textarea — colour, shadow, outline — cannot move text and is left off
 * so the mirror stays cheap. Padding is mirrored because it shifts the
 * first line; the box model is normalised to `content-box` below so the
 * width we set means the same thing in both elements.
 */
const MIRRORED_PROPERTIES = [
  'padding-block-start',
  'padding-block-end',
  'padding-inline-start',
  'padding-inline-end',
  'font-family',
  'font-size',
  'font-weight',
  'font-style',
  'font-variant',
  'letter-spacing',
  'word-spacing',
  'line-height',
  'text-indent',
  'text-transform',
  'tab-size',
  'direction',
] as const;

/**
 * Fallback line height when the computed value is `normal` or a unit the
 * environment does not resolve — a test DOM with no layout engine, for
 * one. Roughly the ratio a browser uses for `normal`.
 */
const NORMAL_LINE_HEIGHT_RATIO = 1.2;

/** Line height of last resort, in CSS pixels, when even the font size is unresolved. */
const FALLBACK_LINE_HEIGHT = 16;

function toNumber(value: string): number {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

/**
 * Measure the character at `index` in `textarea`.
 *
 * Falls back to the textarea's own top-left corner when the environment
 * resolves no layout, so a caller always has somewhere to anchor rather
 * than a `null` it would have to branch on.
 */
export function caretRect(textarea: HTMLTextAreaElement, index: number): CaretRect {
  const box = textarea.getBoundingClientRect();
  const computed = window.getComputedStyle(textarea);

  const lineHeight =
    toNumber(computed.lineHeight) ||
    toNumber(computed.fontSize) * NORMAL_LINE_HEIGHT_RATIO ||
    FALLBACK_LINE_HEIGHT;

  const mirror = document.createElement('div');
  for (const property of MIRRORED_PROPERTIES) {
    mirror.style.setProperty(property, computed.getPropertyValue(property));
  }
  mirror.style.boxSizing = 'content-box';
  mirror.style.position = 'absolute';
  mirror.style.visibility = 'hidden';
  mirror.style.whiteSpace = 'pre-wrap';
  mirror.style.overflowWrap = 'break-word';
  mirror.style.overflow = 'hidden';
  mirror.style.insetBlockStart = '0';
  mirror.style.insetInlineStart = '0';

  // `clientWidth` is the padding box; the mirror carries the same padding
  // on a content box, so the text wraps at the same column.
  const paddingInline =
    toNumber(computed.getPropertyValue('padding-inline-start')) +
    toNumber(computed.getPropertyValue('padding-inline-end'));
  mirror.style.inlineSize = `${String(Math.max(0, textarea.clientWidth - paddingInline))}px`;

  mirror.textContent = textarea.value.slice(0, index);
  const marker = document.createElement('span');
  // A marker with no content collapses and reports the offset of the
  // line it would have opened rather than the one it sits on. The rest
  // of the body gives it the same wrapping the real text has.
  marker.textContent = textarea.value.slice(index) || '.';
  mirror.appendChild(marker);

  document.body.appendChild(mirror);
  // Offsets are measured from inside the offset parent's border, and the
  // mirror carries none, so the textarea's own borders are added back
  // against its outer box.
  const offsetTop = marker.offsetTop;
  const offsetLeft = marker.offsetLeft;
  document.body.removeChild(mirror);

  const top = box.top + toNumber(computed.borderTopWidth) + offsetTop - textarea.scrollTop;
  const left = box.left + toNumber(computed.borderLeftWidth) + offsetLeft - textarea.scrollLeft;
  return { left, top, bottom: top + lineHeight };
}
