#!/usr/bin/env node
// check-error-toasts — every error-path toast must be built from the error
// that was caught.
//
// When a mutation fails, the toast the user sees must say what the API
// refused. A handler that throws the error away and shows a fixed
// translated sentence hides the code and the detail the API returned — from
// the user, and from whoever is reading the bug report afterwards.
//
// This walks every source file under each app's `src/` rather than a
// written-down list of files, so a new component with the same defect is
// caught the day it lands. The handlers it looks at are `catch` blocks,
// `.catch()` callbacks and react-query `onError` callbacks that raise a
// toast; a handler that raises no toast is not this check's business.
//
// The two apps reach the user's language through different helpers, so the
// accepted set is declared per source root below. What counts as "the toast
// was built from the error" is either one of those helpers appearing in the
// handler body, or the handler's own bound identifier being read inside it.
//
// An error path that genuinely has nothing to surface (the failure never
// reached the API) opts out with a comment carrying a reason:
//
//   } catch {
//     // error-toast-exempt: <why the caught value is not worth showing>
//     toaster.show({ tone: 'danger', message: t('...') });
//   }
//
// The marker is recognised as the first thing inside the handler body, or on
// the line directly above the handler. A marker with no reason does not
// count — an exemption nobody justified is indistinguishable from the defect
// it hides.
//
// The self-verification cases at the bottom run on every invocation, before
// the real scan, and a failure among them stops the run. They are the reason
// this lives in a script rather than a test: a control that can be skipped
// with `-t` or an `.only` elsewhere in the suite is a control that will
// eventually be skipped.
//
// Usage:
//   node scripts/check-error-toasts.mjs

import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

/**
 * Source roots to scan, each with the helpers that count as turning a
 * refusal into something the user can read.
 *
 * flow-web resolves a caught value straight to a message; accounts-web has
 * no such single helper — it maps a refusal to an i18n key
 * (`mapAuthThrown` for a thrown value, `mapAuthError` for a parsed
 * problem+json body) or reads the catalog code off it (`refusalCode`) and
 * builds the sentence around that.
 */
const ROOTS = [
  { dir: 'apps/flow-web/src', formatters: ['formatApiError'] },
  { dir: 'apps/accounts-web/src', formatters: ['mapAuthThrown', 'mapAuthError', 'refusalCode'] },
];

const TOAST_CALL = 'toaster.show';
const EXEMPT_MARKER = 'error-toast-exempt:';
const MIN_REASON_LENGTH = 8;

function isIdentifierChar(ch) {
  if (ch === undefined) return false;
  if (ch >= 'a' && ch <= 'z') return true;
  if (ch >= 'A' && ch <= 'Z') return true;
  if (ch >= '0' && ch <= '9') return true;
  return ch === '_' || ch === '$';
}

function isSpace(ch) {
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
function blankOutNonCode(source) {
  const out = source.split('');
  const blank = (from, to) => {
    for (let k = from; k < to; k += 1) {
      if (out[k] !== '\n') out[k] = ' ';
    }
  };

  /** Blank template-literal text from `from` up to the backtick or the `${`. */
  const consumeTemplateText = (from) => {
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
  const holeDepths = [];
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

function skipSpace(code, from) {
  let i = from;
  while (i < code.length && isSpace(code[i])) i += 1;
  return i;
}

/** Offset of the bracket closing the one that opens at `open`, or -1. */
function closingBracket(code, open, openCh, closeCh) {
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
function boundName(params) {
  const start = skipSpace(params, 0);
  let i = start;
  while (i < params.length && isIdentifierChar(params[i])) i += 1;
  const name = params.slice(start, i);
  return name.length > 0 ? name : null;
}

/** Parse an arrow function whose parameter list starts at `from`. */
function parseArrow(code, from) {
  let i = skipSpace(code, from);
  if (code.startsWith('async', i) && !isIdentifierChar(code[i + 5])) i = skipSpace(code, i + 5);
  let param = null;
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

function offsetsOf(haystack, needle) {
  const hits = [];
  let i = haystack.indexOf(needle);
  while (i !== -1) {
    hits.push(i);
    i = haystack.indexOf(needle, i + needle.length);
  }
  return hits;
}

function referencesIdentifier(body, name) {
  for (const at of offsetsOf(body, name)) {
    if (!isIdentifierChar(body[at - 1]) && !isIdentifierChar(body[at + name.length])) return true;
  }
  return false;
}

/**
 * Error handlers in `source` that raise a toast: `catch` blocks, `.catch()`
 * callbacks and mutation `onError` callbacks.
 */
function findErrorToastSites(source) {
  const code = blankOutNonCode(source);
  const sites = [];

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
    let param = null;
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
function usesCaughtError(source, site, formatters) {
  const body = blankOutNonCode(source).slice(site.bodyStart, site.bodyEnd);
  if (formatters.some((name) => body.includes(name))) return true;
  if (site.param === null) return false;
  return referencesIdentifier(body, site.param);
}

function readMarker(line) {
  const at = line.indexOf(EXEMPT_MARKER);
  if (at === -1) return null;
  const commentAt = line.indexOf('//');
  if (commentAt === -1 || commentAt > at) return null;
  const reason = line.slice(at + EXEMPT_MARKER.length).trim();
  return reason.length >= MIN_REASON_LENGTH ? reason : null;
}

/** Marker on the first non-blank line at or after `from`. */
function markerOnLine(source, from) {
  if (from < 0) return null;
  const start = skipSpace(source, from);
  const nl = source.indexOf('\n', start);
  return readMarker(source.slice(start, nl === -1 ? source.length : nl));
}

/** Marker on the last non-blank line before `at`. */
function markerOnPrecedingLine(source, at) {
  const lineStart = source.lastIndexOf('\n', at) + 1;
  if (lineStart === 0) return null;
  const prevEnd = lineStart - 1;
  const prevStart = source.lastIndexOf('\n', prevEnd - 1) + 1;
  return readMarker(source.slice(prevStart, prevEnd));
}

/** Reason given on an opt-out marker for this handler, or null when absent. */
function exemptionReason(source, site) {
  const inBody = markerOnLine(source, source[site.bodyStart] === '{' ? site.bodyStart + 1 : -1);
  if (inBody !== null) return inBody;
  return markerOnPrecedingLine(source, site.at);
}

function lineNumber(source, offset) {
  let line = 1;
  for (let i = 0; i < offset; i += 1) {
    if (source[i] === '\n') line += 1;
  }
  return line;
}

/** Human-readable violations in one file, empty when the file is clean. */
function violationsIn(source, label, formatters) {
  const through = formatters.map((name) => `${name}()`).join(' / ');
  const found = [];
  for (const site of findErrorToastSites(source)) {
    if (usesCaughtError(source, site, formatters)) continue;
    if (exemptionReason(source, site) !== null) continue;
    const line = lineNumber(source, site.at);
    const bound = site.param === null ? 'binds no error' : `has \`${site.param}\` but ignores it`;
    found.push(
      `${label}:${line} — this ${site.kind} handler ${bound}, so the toast it shows cannot say ` +
        `what the API refused. Bind the error and pass it through ${through}, or explain the ` +
        `exception with a "// ${EXEMPT_MARKER} <reason>" comment.`,
    );
  }
  return found;
}

function collectSourceFiles(root) {
  const files = [];
  let entries;
  try {
    entries = readdirSync(root, { recursive: true, withFileTypes: true });
  } catch {
    return files;
  }
  for (const entry of entries) {
    if (!entry.isFile()) continue;
    if (!entry.name.endsWith('.ts') && !entry.name.endsWith('.tsx')) continue;
    if (entry.name.endsWith('.test.ts') || entry.name.endsWith('.test.tsx')) continue;
    const full = join(entry.parentPath, entry.name);
    if (full.includes(`${sep}__tests__${sep}`)) continue;
    files.push(full);
  }
  return files;
}

// ---------------------------------------------------------------------------
// Self-verification. Runs before the scan, every time.
// ---------------------------------------------------------------------------

const SAMPLE_FORMATTERS = ['formatApiError'];

function selfCheck() {
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

  const cases = [
    [
      'reports a catch block that throws the error away',
      () => assert.equal(violationsIn(CATCH_DISCARDS, 'sample.tsx', SAMPLE_FORMATTERS).length, 1),
    ],
    [
      'reports a .catch callback that throws the error away',
      () =>
        assert.equal(
          violationsIn(PROMISE_CATCH_DISCARDS, 'sample.tsx', SAMPLE_FORMATTERS).length,
          1,
        ),
    ],
    [
      'reports an onError callback that throws the error away',
      () =>
        assert.equal(violationsIn(ON_ERROR_DISCARDS, 'sample.tsx', SAMPLE_FORMATTERS).length, 1),
    ],
    [
      'reports a bound-but-unused error',
      () => assert.equal(violationsIn(BOUND_BUT_UNUSED, 'sample.tsx', SAMPLE_FORMATTERS).length, 1),
    ],
    [
      'names the file and the line of the offending handler',
      () => {
        const [message] = violationsIn(ON_ERROR_DISCARDS, 'sample.tsx', SAMPLE_FORMATTERS);
        assert.match(message, /sample\.tsx:4/);
        assert.match(message, /onError/);
      },
    ],
    [
      'accepts a handler that formats the caught error',
      () => {
        const source = `
          async function save(t) {
            try {
              await mutate();
            } catch (err) {
              toaster.show({ tone: 'danger', message: formatApiError(err, t, 'save.error') });
            }
          }
        `;
        assert.deepEqual(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS), []);
      },
    ],
    [
      'accepts a handler that reads the caught error itself',
      () => {
        const source = `
          function save(t) {
            mutation.mutate(input, {
              onError: (err) => {
                toaster.show({ tone: 'danger', message: describe(err) });
              },
            });
          }
        `;
        assert.deepEqual(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS), []);
      },
    ],
    [
      'ignores a catch block that shows no toast',
      () => {
        const source = `
          async function save() {
            try {
              await mutate();
            } catch {
              setFailed(true);
            }
          }
        `;
        assert.deepEqual(findErrorToastSites(source), []);
      },
    ],
    [
      'does not read the word catch out of a string or a comment',
      () => {
        const source = `
          // catch { toaster.show(nothing) }
          const help = "catch { toaster.show(nothing) }";
        `;
        assert.deepEqual(findErrorToastSites(source), []);
      },
    ],
    [
      'honours an opt-out marker inside the handler',
      () => {
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
        assert.deepEqual(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS), []);
      },
    ],
    [
      'honours an opt-out marker above the handler',
      () => {
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
        assert.deepEqual(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS), []);
      },
    ],
    [
      'rejects an opt-out marker with no reason',
      () => {
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
        assert.equal(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS).length, 1);
      },
    ],
    [
      'accepts a formatter the root declares and rejects one it does not',
      () => {
        const source = `
          async function save(t) {
            try {
              await mutate();
            } catch {
              toaster.show({ tone: 'danger', message: t(mapAuthThrown(thrown)) });
            }
          }
        `;
        assert.deepEqual(violationsIn(source, 'sample.tsx', ['mapAuthThrown']), []);
        assert.equal(violationsIn(source, 'sample.tsx', SAMPLE_FORMATTERS).length, 1);
      },
    ],
  ];

  const failures = [];
  for (const [name, run] of cases) {
    try {
      run();
    } catch (err) {
      failures.push(`  ${name}\n    ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  return failures;
}

const selfFailures = selfCheck();
if (selfFailures.length > 0) {
  console.error(
    `check-error-toasts: ${selfFailures.length} self-verification case(s) failed, so the scan was not run:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the sources.',
  );
  process.exit(1);
}

// ---------------------------------------------------------------------------
// The scan.
// ---------------------------------------------------------------------------

const findings = [];
const empty = [];
let totalFiles = 0;
let totalSites = 0;

for (const { dir, formatters } of ROOTS) {
  const files = collectSourceFiles(join(repo, dir));
  let sites = 0;
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    sites += findErrorToastSites(source).length;
    findings.push(...violationsIn(source, relative(repo, file), formatters));
  }
  totalFiles += files.length;
  totalSites += sites;
  // A mistyped root would silently check nothing and pass forever.
  if (files.length === 0) empty.push(`${dir} — no .ts/.tsx sources found`);
  else if (sites === 0) empty.push(`${dir} — no error-path toast sites found`);
}

if (empty.length > 0) {
  console.error(`check-error-toasts: ${empty.length} source root(s) yielded nothing to check:\n`);
  for (const e of empty) console.error(`  ${e}`);
  console.error(
    '\nA root that finds nothing proves nothing. Either the path in ROOTS is wrong, or the app',
  );
  console.error('stopped raising toasts from its error paths and this root should be dropped.');
  process.exit(1);
}

if (findings.length > 0) {
  console.error(
    `check-error-toasts: ${findings.length} error-path toast(s) do not say what the API refused:\n`,
  );
  for (const f of findings) console.error(`  ${f}`);
  console.error(
    '\nA fixed translated sentence hides the code and detail the API returned, from the user and',
  );
  console.error(
    'from whoever reads the bug report. Bind the caught error and build the message from it. An',
  );
  console.error(
    'error path that never reached the API takes a "// error-toast-exempt: <reason>" comment.',
  );
  process.exit(1);
}

console.info(
  `check-error-toasts: every error-path toast is built from the caught error (${totalSites} handler(s) across ${totalFiles} file(s))`,
);
