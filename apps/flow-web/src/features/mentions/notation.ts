/**
 * The notation a mention is written in inside a task description or a
 * task comment:
 *
 *     @[Display Name](user:019649b0-0000-7000-8000-000000000000)
 *
 * It is an ordinary markdown link carrying an id. Only the id is
 * resolved — the display name between the brackets is decoration, which
 * is what keeps a mention pointing at the same person after a rename and
 * unambiguous between two people who chose the same name.
 *
 * A bare `@alice` is deliberately not a mention. The picker is the only
 * thing that writes the stable form, so everything in this module is
 * about finding the moment the author is asking for one and producing
 * exactly the text the backend reads back.
 */

/** Longest run of characters after `@` still treated as a search query. */
const MAX_QUERY_LENGTH = 40;

/** A mention the author is part-way through typing. */
export interface MentionQuery {
  /** Index of the `@` that opened it. */
  start: number;
  /** Text typed after the `@`, up to the caret. Empty right after `@`. */
  query: string;
}

/** Result of writing a mention into a body. */
export interface MentionInsertion {
  /** The body with the notation written in. */
  value: string;
  /** Where the caret belongs afterwards. */
  caret: number;
}

function isSpace(ch: string): boolean {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r';
}

/**
 * Characters that make the `@` after them part of a word rather than the
 * start of a mention — the local part of an email address, a handle
 * already written out. Latin letters, digits, and the punctuation an
 * address is built from.
 *
 * CJK characters are deliberately absent: Japanese and Chinese prose puts
 * no space before an `@`, so treating a preceding kanji as "mid-word"
 * would mean the picker never opens for the languages this product is
 * written in first.
 */
function isLatinWordChar(ch: string): boolean {
  const code = ch.codePointAt(0);
  if (code === undefined) return false;
  const isDigit = code >= 0x30 && code <= 0x39;
  const isUpper = code >= 0x41 && code <= 0x5a;
  const isLower = code >= 0x61 && code <= 0x7a;
  return isDigit || isUpper || isLower || ch === '.' || ch === '_' || ch === '%' || ch === '+';
}

/**
 * Find the mention the caret sits inside, or `null` when it sits in
 * ordinary prose.
 *
 * Returning `null` is what closes the picker, so every case that should
 * leave the author's text alone reaches it: a space typed after the `@`,
 * an `@` in the middle of an email address, a query grown past any
 * plausible name, and a caret already inside a completed notation.
 */
export function findMentionQuery(value: string, caret: number): MentionQuery | null {
  if (caret < 0 || caret > value.length) return null;

  const floor = Math.max(0, caret - (MAX_QUERY_LENGTH + 1));
  let start = -1;
  for (let i = caret - 1; i >= floor; i -= 1) {
    const ch = value[i];
    if (ch === undefined) return null;
    if (ch === '@') {
      start = i;
      break;
    }
    if (isSpace(ch)) return null;
  }
  if (start < 0) return null;

  if (start > 0) {
    const before = value[start - 1];
    if (before !== undefined && isLatinWordChar(before)) return null;
  }

  const query = value.slice(start + 1, caret);
  // `@[` opens a notation the picker already wrote. Reopening on it would
  // filter members by the display name that is sitting there.
  if (query.startsWith('[')) return null;

  return { start, query };
}

/**
 * Escape the characters that would end the link text early when the body
 * is rendered. The backend carries a `]` inside a display name through to
 * the id, but a markdown reader stops the label at the first one, so the
 * name on screen would lose its tail.
 */
function escapeDisplayName(displayName: string): string {
  let out = '';
  for (const ch of displayName) {
    if (ch === '\\' || ch === '[' || ch === ']') out += `\\${ch}`;
    else if (ch === '\n' || ch === '\r' || ch === '\t') out += ' ';
    else out += ch;
  }
  return out;
}

/** Build the stable notation for one person. */
export function formatMention(displayName: string, userId: string): string {
  return `@[${escapeDisplayName(displayName)}](user:${userId})`;
}

/**
 * Replace the part-way-typed mention that starts at `start` and ends at
 * the caret with the stable notation, followed by a space so the next
 * word does not run into the closing parenthesis — and so the picker does
 * not immediately reopen on what was just written. A space already there
 * is not doubled.
 */
export function insertMention(
  value: string,
  start: number,
  caret: number,
  displayName: string,
  userId: string,
): MentionInsertion {
  const notation = formatMention(displayName, userId);
  const followedBySpace = value[caret] === ' ';
  const written = followedBySpace ? notation : `${notation} `;
  return {
    value: `${value.slice(0, start)}${written}${value.slice(caret)}`,
    caret: start + written.length,
  };
}
