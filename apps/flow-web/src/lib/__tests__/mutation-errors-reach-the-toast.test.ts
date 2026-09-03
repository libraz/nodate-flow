import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

/**
 * When a mutation fails, the toast the user sees must be built from the error
 * that was caught. A handler that throws the error away and shows a fixed
 * translated sentence hides the code and detail the API returned — from the
 * user and from whoever is reading the bug report afterwards.
 *
 * This walks every source file under `src/` rather than a written-down list of
 * files, so a new component with the same defect is caught the day it lands.
 *
 * An error path that genuinely has nothing to surface (the failure never
 * reached the API) opts out with a comment carrying a reason:
 *
 *   } catch {
 *     // error-toast-exempt: <why the caught value is not worth showing>
 *     toaster.show({ tone: 'danger', message: t('...') });
 *   }
 *
 * The marker is recognised as the first thing inside the handler body, or on
 * the line directly above the handler. A marker with no reason does not count.
 */

const SRC_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

const TOAST_CALL = 'toaster.show';
const FORMATTER = 'formatApiError';
const EXEMPT_MARKER = 'error-toast-exempt:';
const MIN_REASON_LENGTH = 8;

type SiteKind = 'catch' | '.catch' | 'onError';

interface Site {
  kind: SiteKind;
  /** Offset of the `catch` / `onError` token that opens the handler. */
  at: number;
  /** Identifier the caught error is bound to, or null when nothing is bound. */
  param: string | null;
  bodyStart: number;
  bodyEnd: number;
}

function isIdentifierChar(ch: string | undefined): boolean {
  if (ch === undefined) return false;
  if (ch >= 'a' && ch <= 'z') return true;
  if (ch >= 'A' && ch <= 'Z') return true;
  if (ch >= '0' && ch <= '9') return true;
  return ch === '_' || ch === '$';
}

function isSpace(ch: string | undefined): boolean {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r';
}

/**
 * Replace comment and string-literal contents with spaces, keeping every
 * offset (and every newline) where it was, so the result can be searched for
 * code without matching the word `catch` inside a sentence or a message.
 * Template-literal `${...}` holes stay intact — they are code.
 *
 * Bare apostrophes in JSX text would confuse this, but the project bans
 * hard-coded UI strings, so JSX text nodes are `{t('key')}` calls.
 */
function blankOutNonCode(source: string): string {
  const out = source.split('');
  const blank = (from: number, to: number): void => {
    for (let k = from; k < to; k += 1) {
      if (out[k] !== '\n') out[k] = ' ';
    }
  };

  /** Blank template-literal text from `from` up to the backtick or the `${`. */
  const consumeTemplateText = (from: number): { next: number; hole: boolean } => {
    let j = from;
    while (j < source.length) {
      const d = source[j];
      if (d === '\\') {
        j += 2;
        continue;
      }
      if (d === '`') {
        blank(from, j);
        return { next: j + 1, hole: false };
      }
      if (d === '$' && source[j + 1] === '{') {
        blank(from, j);
        return { next: j + 2, hole: true };
      }
      j += 1;
    }
    blank(from, j);
    return { next: j, hole: false };
  };

  // Brace depth at which each open template hole started, so the scanner knows
  // which `}` puts it back inside the literal.
  const holeDepths: number[] = [];
  let depth = 0;
  let i = 0;
  while (i < source.length) {
    const ch = source[i];
    const next = source[i + 1];
    if (ch === '/' && next === '/') {
      const nl = source.indexOf('\n', i);
      const end = nl === -1 ? source.length : nl;
      blank(i, end);
      i = end;
      continue;
    }
    if (ch === '/' && next === '*') {
      const close = source.indexOf('*/', i + 2);
      const end = close === -1 ? source.length : close + 2;
      blank(i, end);
      i = end;
      continue;
    }
    if (ch === "'" || ch === '"') {
      let j = i + 1;
      while (j < source.length) {
        const d = source[j];
        if (d === '\\') {
          j += 2;
          continue;
        }
        if (d === ch || d === '\n') break;
        j += 1;
      }
      blank(i + 1, j);
      i = j + 1;
      continue;
    }
    if (ch === '`') {
      const stop = consumeTemplateText(i + 1);
      i = stop.next;
      if (stop.hole) {
        holeDepths.push(depth);
        depth += 1;
      }
      continue;
    }
    if (ch === '{') {
      depth += 1;
      i += 1;
      continue;
    }
    if (ch === '}') {
      depth -= 1;
      i += 1;
      if (holeDepths[holeDepths.length - 1] === depth) {
        holeDepths.pop();
        const stop = consumeTemplateText(i);
        i = stop.next;
        if (stop.hole) {
          holeDepths.push(depth);
          depth += 1;
        }
      }
      continue;
    }
    i += 1;
  }
  return out.join('');
}

function skipSpace(code: string, from: number): number {
  let i = from;
  while (i < code.length && isSpace(code[i])) i += 1;
  return i;
}

/** Offset of the bracket closing the one that opens at `open`, or -1. */
function closingBracket(code: string, open: number, openCh: string, closeCh: string): number {
  let depth = 0;
  for (let i = open; i < code.length; i += 1) {
    if (code[i] === openCh) depth += 1;
    else if (code[i] === closeCh) {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/** First parameter name in a parameter list, or null for none / destructuring. */
function boundName(params: string): string | null {
  const start = skipSpace(params, 0);
  let i = start;
  while (i < params.length && isIdentifierChar(params[i])) i += 1;
  const name = params.slice(start, i);
  return name.length > 0 ? name : null;
}

interface Arrow {
  param: string | null;
  bodyStart: number;
  bodyEnd: number;
}

/** Parse an arrow function whose parameter list starts at `from`. */
function parseArrow(code: string, from: number): Arrow | null {
  let i = skipSpace(code, from);
  if (code.startsWith('async', i) && !isIdentifierChar(code[i + 5])) i = skipSpace(code, i + 5);
  let param: string | null = null;
  if (code[i] === '(') {
    const close = closingBracket(code, i, '(', ')');
    if (close === -1) return null;
    param = boundName(code.slice(i + 1, close));
    i = skipSpace(code, close + 1);
  } else if (isIdentifierChar(code[i])) {
    const start = i;
    while (i < code.length && isIdentifierChar(code[i])) i += 1;
    param = code.slice(start, i);
    i = skipSpace(code, i);
  } else {
    return null;
  }
  if (!code.startsWith('=>', i)) return null;
  i = skipSpace(code, i + 2);
  if (code[i] === '{') {
    const close = closingBracket(code, i, '{', '}');
    if (close === -1) return null;
    return { param, bodyStart: i, bodyEnd: close + 1 };
  }
  // Expression body: everything up to the end of the enclosing call argument.
  let depth = 0;
  let k = i;
  while (k < code.length) {
    const ch = code[k];
    if (ch === '(' || ch === '[' || ch === '{') depth += 1;
    else if (ch === ')' || ch === ']' || ch === '}') {
      if (depth === 0) break;
      depth -= 1;
    } else if (ch === ',' && depth === 0) break;
    k += 1;
  }
  return { param, bodyStart: i, bodyEnd: k };
}

function offsetsOf(haystack: string, needle: string): number[] {
  const hits: number[] = [];
  let i = haystack.indexOf(needle);
  while (i !== -1) {
    hits.push(i);
    i = haystack.indexOf(needle, i + needle.length);
  }
  return hits;
}

function referencesIdentifier(body: string, name: string): boolean {
  for (const at of offsetsOf(body, name)) {
    if (!isIdentifierChar(body[at - 1]) && !isIdentifierChar(body[at + name.length])) return true;
  }
  return false;
}

/**
 * Error handlers in `source` that raise a toast: `catch` blocks, `.catch()`
 * callbacks and mutation `onError` callbacks.
 */
function findErrorToastSites(source: string): Site[] {
  const code = blankOutNonCode(source);
  const sites: Site[] = [];

  for (const at of offsetsOf(code, 'catch')) {
    const before = code[at - 1];
    const after = code[at + 5];
    if (before === '.') {
      const open = skipSpace(code, at + 5);
      if (code[open] !== '(') continue;
      const arrow = parseArrow(code, open + 1);
      if (arrow) sites.push({ kind: '.catch', at, ...arrow });
      continue;
    }
    if (isIdentifierChar(before) || isIdentifierChar(after)) continue;
    let i = skipSpace(code, at + 5);
    let param: string | null = null;
    if (code[i] === '(') {
      const close = closingBracket(code, i, '(', ')');
      if (close === -1) continue;
      param = boundName(code.slice(i + 1, close));
      i = skipSpace(code, close + 1);
    }
    if (code[i] !== '{') continue;
    const close = closingBracket(code, i, '{', '}');
    if (close === -1) continue;
    sites.push({ kind: 'catch', at, param, bodyStart: i, bodyEnd: close + 1 });
  }

  for (const at of offsetsOf(code, 'onError')) {
    if (isIdentifierChar(code[at - 1])) continue;
    const colon = skipSpace(code, at + 'onError'.length);
    if (code[colon] !== ':') continue;
    const arrow = parseArrow(code, colon + 1);
    if (arrow) sites.push({ kind: 'onError', at, ...arrow });
  }

  return sites.filter((site) => code.slice(site.bodyStart, site.bodyEnd).includes(TOAST_CALL));
}

/** True when the toast in this handler is built from the caught error. */
function usesCaughtError(source: string, site: Site): boolean {
  const body = blankOutNonCode(source).slice(site.bodyStart, site.bodyEnd);
  if (body.includes(FORMATTER)) return true;
  if (site.param === null) return false;
  return referencesIdentifier(body, site.param);
}

/** Reason given on an opt-out marker for this handler, or null when absent. */
function exemptionReason(source: string, site: Site): string | null {
  const inBody = markerOnLine(source, source[site.bodyStart] === '{' ? site.bodyStart + 1 : -1);
  if (inBody !== null) return inBody;
  return markerOnPrecedingLine(source, site.at);
}

function readMarker(line: string): string | null {
  const at = line.indexOf(EXEMPT_MARKER);
  if (at === -1) return null;
  const commentAt = line.indexOf('//');
  if (commentAt === -1 || commentAt > at) return null;
  const reason = line.slice(at + EXEMPT_MARKER.length).trim();
  return reason.length >= MIN_REASON_LENGTH ? reason : null;
}

/** Marker on the first non-blank line at or after `from`. */
function markerOnLine(source: string, from: number): string | null {
  if (from < 0) return null;
  const start = skipSpace(source, from);
  const nl = source.indexOf('\n', start);
  return readMarker(source.slice(start, nl === -1 ? source.length : nl));
}

/** Marker on the last non-blank line before `at`. */
function markerOnPrecedingLine(source: string, at: number): string | null {
  const lineStart = source.lastIndexOf('\n', at) + 1;
  if (lineStart === 0) return null;
  const prevEnd = lineStart - 1;
  const prevStart = source.lastIndexOf('\n', prevEnd - 1) + 1;
  return readMarker(source.slice(prevStart, prevEnd));
}

function lineNumber(source: string, offset: number): number {
  let line = 1;
  for (let i = 0; i < offset; i += 1) {
    if (source[i] === '\n') line += 1;
  }
  return line;
}

/** Human-readable violations in one file, empty when the file is clean. */
function violationsIn(source: string, label: string): string[] {
  const found: string[] = [];
  for (const site of findErrorToastSites(source)) {
    if (usesCaughtError(source, site)) continue;
    if (exemptionReason(source, site) !== null) continue;
    const line = lineNumber(source, site.at);
    const bound = site.param === null ? 'binds no error' : `has \`${site.param}\` but ignores it`;
    found.push(
      `${label}:${line} — this ${site.kind} handler ${bound}, so the toast it shows cannot say ` +
        `what the API refused. Bind the error and pass it through ${FORMATTER}(), or explain the ` +
        `exception with a "// ${EXEMPT_MARKER} <reason>" comment.`,
    );
  }
  return found;
}

function collectSourceFiles(root: string): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(root, { recursive: true, withFileTypes: true })) {
    if (!entry.isFile()) continue;
    if (!entry.name.endsWith('.ts') && !entry.name.endsWith('.tsx')) continue;
    if (entry.name.endsWith('.test.ts') || entry.name.endsWith('.test.tsx')) continue;
    const full = join(entry.parentPath, entry.name);
    if (full.includes(`${sep}__tests__${sep}`)) continue;
    files.push(full);
  }
  return files;
}

describe('mutation errors reach the toast', () => {
  const files = collectSourceFiles(SRC_ROOT);

  it('finds the flow-web sources to check', () => {
    // A mistyped root would silently check nothing and pass forever.
    expect(files.length).toBeGreaterThan(0);
  });

  it('finds error-path toasts to check', () => {
    const sites = files.reduce(
      (n, file) => n + findErrorToastSites(readFileSync(file, 'utf8')).length,
      0,
    );
    expect(sites).toBeGreaterThan(0);
  });

  it('builds every error-path toast from the caught error', () => {
    const violations = files.flatMap((file) =>
      violationsIn(readFileSync(file, 'utf8'), relative(SRC_ROOT, file)),
    );
    expect(violations).toEqual([]);
  });
});

describe('the check itself', () => {
  const CATCH_DISCARDS = `
    async function save(t) {
      try {
        await mutate();
      } catch {
        toaster.show({ tone: 'danger', message: t('save.error') });
      }
    }
  `;

  const PROMISE_CATCH_DISCARDS = `
    function save(t) {
      void mutate().catch(() => {
        toaster.show({ tone: 'danger', message: t('save.error') });
      });
    }
  `;

  const ON_ERROR_DISCARDS = `
    function save(t) {
      mutation.mutate(input, {
        onError: () => {
          toaster.show({ tone: 'danger', message: t('save.error') });
        },
      });
    }
  `;

  const BOUND_BUT_UNUSED = `
    async function save(t) {
      try {
        await mutate();
      } catch (err) {
        toaster.show({ tone: 'danger', message: t('save.error') });
      }
    }
  `;

  it.each([
    ['a catch block', CATCH_DISCARDS],
    ['a .catch callback', PROMISE_CATCH_DISCARDS],
    ['an onError callback', ON_ERROR_DISCARDS],
    ['a bound-but-unused error', BOUND_BUT_UNUSED],
  ])('reports %s that throws the error away', (_label, source) => {
    expect(violationsIn(source, 'sample.tsx')).toHaveLength(1);
  });

  it('names the file and the line of the offending handler', () => {
    const [message] = violationsIn(ON_ERROR_DISCARDS, 'sample.tsx');
    expect(message).toContain('sample.tsx:4');
    expect(message).toContain('onError');
  });

  it('accepts a handler that formats the caught error', () => {
    const source = `
      async function save(t) {
        try {
          await mutate();
        } catch (err) {
          toaster.show({ tone: 'danger', message: formatApiError(err, t, 'save.error') });
        }
      }
    `;
    expect(violationsIn(source, 'sample.tsx')).toEqual([]);
  });

  it('accepts a handler that reads the caught error itself', () => {
    const source = `
      function save(t) {
        mutation.mutate(input, {
          onError: (err) => {
            toaster.show({ tone: 'danger', message: describe(err) });
          },
        });
      }
    `;
    expect(violationsIn(source, 'sample.tsx')).toEqual([]);
  });

  it('ignores a catch block that shows no toast', () => {
    const source = `
      async function save() {
        try {
          await mutate();
        } catch {
          setFailed(true);
        }
      }
    `;
    expect(findErrorToastSites(source)).toEqual([]);
  });

  it('does not read the word catch out of a string or a comment', () => {
    const source = `
      // catch { toaster.show(nothing) }
      const help = "catch { toaster.show(nothing) }";
    `;
    expect(findErrorToastSites(source)).toEqual([]);
  });

  it('honours an opt-out marker inside the handler', () => {
    const source = `
      async function copy(t) {
        try {
          await navigator.clipboard.writeText(url);
        } catch {
          // error-toast-exempt: the clipboard never reached the API.
          toaster.show({ tone: 'danger', message: t('copy.error') });
        }
      }
    `;
    expect(violationsIn(source, 'sample.tsx')).toEqual([]);
  });

  it('honours an opt-out marker above the handler', () => {
    const source = `
      function save(t) {
        mutation.mutate(input, {
          // error-toast-exempt: the caller already toasted the API detail.
          onError: () => {
            toaster.show({ tone: 'danger', message: t('save.error') });
          },
        });
      }
    `;
    expect(violationsIn(source, 'sample.tsx')).toEqual([]);
  });

  it('rejects an opt-out marker with no reason', () => {
    const source = `
      async function copy(t) {
        try {
          await mutate();
        } catch {
          // error-toast-exempt:
          toaster.show({ tone: 'danger', message: t('copy.error') });
        }
      }
    `;
    expect(violationsIn(source, 'sample.tsx')).toHaveLength(1);
  });
});
