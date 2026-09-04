#!/usr/bin/env node
// check-error-toasts — a failed request must reach the person who caused it.
//
// Two rules, both about the same thing from opposite ends.
//
// One: every error-path toast must be built from the error that was caught.
// When a mutation fails, the toast the user sees must say what the API
// refused. A handler that throws the error away and shows a fixed
// translated sentence hides the code and the detail the API returned — from
// the user, and from whoever is reading the bug report afterwards. The
// handlers it looks at are `catch` blocks, `.catch()` callbacks and
// react-query `onError` callbacks that raise a toast; a handler that raises
// no toast is not this rule's business.
//
// Two: every `.mutate(` call site must have an error path at all. Mutations
// run with `throwOnError: false`, so a `.mutate()` with no `onError` — not at
// the call site, not in the hook it came from — discards its failure where
// nobody can see it: nothing throws, nothing is caught, no toast, no console.
// The user clicks and nothing happens. Rule one cannot see this, because
// there is no handler to inspect. A hook `onError` that only repairs an
// optimistic cache write does not count: it restores the screen, it says
// nothing. Nor does an enclosing `try` — with `throwOnError: false` the catch
// never fires, and that shape is reported by name.
//
// This walks every source file under each app's `src/` rather than a
// written-down list of files, so a new component with the same defect is
// caught the day it lands.
//
// The two apps reach the user's language through different helpers, so the
// accepted set is declared per source root below. What counts as "the toast
// was built from the error" is either one of those helpers appearing in the
// handler body, or the handler's own bound identifier being read inside it.
//
// A failure that genuinely has nothing to surface — one that never reached
// the API, or bookkeeping behind an action that already reported its own
// outcome — opts out with a comment carrying a reason:
//
//   } catch {
//     // error-toast-exempt: <why the caught value is not worth showing>
//     toaster.show({ tone: 'danger', message: t('...') });
//   }
//
// For a handler the marker is recognised as the first thing inside its body
// or on the line directly above it; for a `.mutate(` call, on the line
// directly above the call. A marker with no reason does not count — an
// exemption nobody justified is indistinguishable from the defect it hides.
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
import { dirname, join, relative, resolve, sep } from 'node:path';
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
  { dir: 'apps/flow-web/src', formatters: ['formatApiError'], mutateSites: true },
  {
    dir: 'apps/accounts-web/src',
    formatters: ['mapAuthThrown', 'mapAuthError', 'refusalCode'],
    // accounts-web drives every mutation through `mutateAsync` and its own
    // submit guard, so finding no `.mutate(` here is the expected state, not a
    // broken scan. A site that does appear is still checked.
    mutateSites: false,
  },
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
 * Split `code[from, to)` on the commas that sit outside every bracket, giving
 * the spans of an argument list or of an object literal's members.
 */
function splitTopLevel(code, from, to) {
  const parts = [];
  let depth = 0;
  let start = from;
  for (let i = from; i < to; i += 1) {
    const ch = code[i];
    if (ch === '(' || ch === '[' || ch === '{') depth += 1;
    else if (ch === ')' || ch === ']' || ch === '}') depth -= 1;
    else if (ch === ',' && depth === 0) {
      parts.push([start, i]);
      start = i + 1;
    }
  }
  if (skipSpace(code, start) < to) parts.push([start, to]);
  return parts;
}

/**
 * Own properties of the object literal opening at `braceOpen`, each with the
 * span of its value. `hasSpread` marks a `...rest` member, which can carry a
 * property this cannot see.
 */
function objectProperties(code, braceOpen) {
  const close = closingBracket(code, braceOpen, '{', '}');
  if (close === -1) return null;
  const props = [];
  let hasSpread = false;
  for (const [from, to] of splitTopLevel(code, braceOpen + 1, close)) {
    const start = skipSpace(code, from);
    if (start >= to) continue;
    if (code.startsWith('...', start)) {
      hasSpread = true;
      continue;
    }
    let i = start;
    while (i < to && isIdentifierChar(code[i])) i += 1;
    if (i === start) continue;
    const name = code.slice(start, i);
    const colon = skipSpace(code, i);
    if (code[colon] === ':') props.push({ name, valueStart: colon + 1, valueEnd: to });
    else props.push({ name, valueStart: start, valueEnd: i });
  }
  return { props, hasSpread, close };
}

/**
 * Offset just past the `<...>` type arguments at `from`, or `from` when there
 * are none. `=>` is stepped over so a function type inside the list does not
 * close it early, and a `;` only ends the search outside an object type — the
 * separator inside `{ a: string; b: number }` is not the end of a statement.
 */
function skipTypeArguments(code, from) {
  let i = skipSpace(code, from);
  if (code[i] !== '<') return from;
  let depth = 0;
  let braces = 0;
  while (i < code.length) {
    const ch = code[i];
    if (ch === '=' && code[i + 1] === '>') {
      i += 2;
      continue;
    }
    if (ch === '<') depth += 1;
    else if (ch === '>') {
      depth -= 1;
      if (depth === 0) return i + 1;
    } else if (ch === '{') braces += 1;
    else if (ch === '}') braces -= 1;
    else if (ch === ';' && braces === 0) return from;
    i += 1;
  }
  return from;
}

/**
 * Offset of the `{` that opens a function body, given the offset just past its
 * parameter list. A return-type annotation is skipped, including one whose
 * type arguments contain an object type of their own.
 */
function functionBodyBrace(code, from) {
  let angle = 0;
  for (let i = from; i < code.length; i += 1) {
    const ch = code[i];
    if (ch === '=' && code[i + 1] === '>') {
      i += 1;
      continue;
    }
    if (ch === '<') angle += 1;
    else if (ch === '>') {
      if (angle > 0) angle -= 1;
    } else if (angle === 0 && ch === '{') return i;
    else if (angle === 0 && ch === ';') return -1;
  }
  return -1;
}

/** The call `name(` starting at `from`, or null when `from` holds something else. */
function callAt(code, from) {
  let i = skipSpace(code, from);
  const start = i;
  while (i < code.length && isIdentifierChar(code[i])) i += 1;
  if (i === start) return null;
  const name = code.slice(start, i);
  const open = skipSpace(code, skipTypeArguments(code, i));
  if (code[open] !== '(') return null;
  const close = closingBracket(code, open, '(', ')');
  if (close === -1) return null;
  return { name, argsStart: open + 1, argsEnd: close };
}

/** Offset just past the `=` of a `const`/`let`/`var` declarator for `name`. */
function declaratorInit(code, name) {
  for (const at of offsetsOf(code, name)) {
    if (isIdentifierChar(code[at - 1]) || isIdentifierChar(code[at + name.length])) continue;
    const before = code.slice(Math.max(0, at - 24), at);
    if (!/\b(?:const|let|var)\s+$/.test(before)) continue;
    let i = skipSpace(code, at + name.length);
    // A type annotation may sit between the name and the `=`.
    if (code[i] === ':') {
      let depth = 0;
      while (i < code.length) {
        const ch = code[i];
        if (ch === '(' || ch === '[' || ch === '{' || ch === '<') depth += 1;
        else if (ch === ')' || ch === ']' || ch === '}' || ch === '>') depth -= 1;
        else if (ch === '=' && depth === 0) break;
        i += 1;
      }
    }
    if (code[i] !== '=' || code[i + 1] === '=' || code[i + 1] === '>') continue;
    return i + 1;
  }
  return null;
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

// ---------------------------------------------------------------------------
// Rule two: a `.mutate(` call site must have an error path.
// ---------------------------------------------------------------------------

const MUTATE_METHOD = '.mutate';
const MUTATION_HOOK = 'useMutation';

/** `.mutate(` call sites, with the receiver each was called on. */
function findMutateCallSites(source) {
  const code = blankOutNonCode(source);
  const sites = [];
  for (const at of offsetsOf(code, MUTATE_METHOD)) {
    const open = at + MUTATE_METHOD.length;
    if (code[open] !== '(') continue; // `.mutateAsync(` and any other suffix
    const close = closingBracket(code, open, '(', ')');
    if (close === -1) continue;
    let i = at - 1;
    if (code[i] === '?') i -= 1; // optional chaining
    const end = i + 1;
    while (i >= 0 && isIdentifierChar(code[i])) i -= 1;
    if (i + 1 === end) continue;
    // A receiver reached through a longer chain has no declarator to follow.
    const chained = code[i] === '.' || code[i] === ')' || code[i] === ']';
    sites.push({
      at,
      receiver: code.slice(i + 1, end),
      chained,
      argsStart: open + 1,
      argsEnd: close,
    });
  }
  return sites;
}

/** Spans of every `try` block that has a `catch`. */
function tryBlockSpans(code) {
  const spans = [];
  for (const at of offsetsOf(code, 'try')) {
    if (isIdentifierChar(code[at - 1]) || isIdentifierChar(code[at + 3])) continue;
    const open = skipSpace(code, at + 3);
    if (code[open] !== '{') continue;
    const close = closingBracket(code, open, '{', '}');
    if (close === -1) continue;
    if (!code.startsWith('catch', skipSpace(code, close + 1))) continue;
    spans.push([open, close]);
  }
  return spans;
}

/** The name a module exports under, for a name imported into this file. */
function importedFrom(source, code, name) {
  for (const at of offsetsOf(code, 'import')) {
    if (isIdentifierChar(code[at - 1]) || isIdentifierChar(code[at + 'import'.length])) continue;
    const clauseStart = at + 'import'.length;
    let fromAt = -1;
    for (const cand of offsetsOf(code, 'from')) {
      if (cand < clauseStart) continue;
      if (isIdentifierChar(code[cand - 1]) || isIdentifierChar(code[cand + 4])) continue;
      fromAt = cand;
      break;
    }
    if (fromAt === -1) continue;
    const clause = code.slice(clauseStart, fromAt);
    // A side-effect import (`import './x'`) has no `from`, so a quote before
    // the one we found means this clause belongs to an earlier statement.
    if (clause.includes("'") || clause.includes('"')) continue;
    const quote = code[skipSpace(code, fromAt + 4)];
    if (quote !== "'" && quote !== '"') continue;
    const specStart = skipSpace(code, fromAt + 4) + 1;
    const specEnd = code.indexOf(quote, specStart);
    if (specEnd === -1) continue;
    const braceAt = clause.indexOf('{');
    if (braceAt === -1) continue;
    const braceEnd = clause.indexOf('}', braceAt);
    if (braceEnd === -1) continue;
    for (const raw of clause.slice(braceAt + 1, braceEnd).split(',')) {
      const entry = raw.trim().replace(/^type\s+/, '');
      if (entry.length === 0) continue;
      const parts = entry.split(/\s+as\s+/);
      const local = (parts[1] ?? parts[0]).trim();
      if (local === name)
        return { spec: source.slice(specStart, specEnd), imported: parts[0].trim() };
    }
  }
  return null;
}

/** Body span of a `function name(...)` or `const name = (...) =>` definition. */
function definitionBody(code, name) {
  for (const at of offsetsOf(code, name)) {
    if (isIdentifierChar(code[at - 1]) || isIdentifierChar(code[at + name.length])) continue;
    if (!/\bfunction\s+$/.test(code.slice(Math.max(0, at - 24), at))) continue;
    const paren = skipSpace(code, skipTypeArguments(code, at + name.length));
    if (code[paren] !== '(') continue;
    const parenClose = closingBracket(code, paren, '(', ')');
    if (parenClose === -1) continue;
    const brace = functionBodyBrace(code, parenClose + 1);
    if (brace === -1) continue;
    const end = closingBracket(code, brace, '{', '}');
    if (end === -1) continue;
    return { bodyStart: brace, bodyEnd: end + 1 };
  }
  const init = declaratorInit(code, name);
  if (init === null) return null;
  const arrow = parseArrow(code, init);
  return arrow === null ? null : { bodyStart: arrow.bodyStart, bodyEnd: arrow.bodyEnd };
}

/**
 * What the options object of one `useMutation(...)` call installs for failure:
 * an `onError` that raises a toast, one that does not (an optimistic-update
 * rollback surfaces nothing to the user), none at all, or something this
 * cannot read.
 */
function mutationCallRoute(code, call) {
  const brace = skipSpace(code, call.argsStart);
  if (code[brace] !== '{') return 'unknown';
  const obj = objectProperties(code, brace);
  if (obj === null) return 'unknown';
  const onError = obj.props.find((p) => p.name === 'onError');
  if (onError !== undefined) {
    return code.slice(onError.valueStart, onError.valueEnd).includes(TOAST_CALL)
      ? 'hook-toast'
      : 'hook-silent';
  }
  return obj.hasSpread ? 'unknown' : 'none';
}

/** The route installed by the `useMutation` calls inside a hook body. */
function hookBodyRoute(code, bodyStart, bodyEnd) {
  let seen = false;
  for (const at of offsetsOf(code, MUTATION_HOOK)) {
    if (at < bodyStart || at >= bodyEnd) continue;
    if (isIdentifierChar(code[at - 1])) continue;
    const call = callAt(code, at);
    if (call === null || call.name !== MUTATION_HOOK) continue;
    seen = true;
    const route = mutationCallRoute(code, call);
    if (route !== 'none') return route;
  }
  return seen ? 'none' : 'unknown';
}

/** A route this cannot read is undetermined, never an answer either way. */
function normalizeRoute(route) {
  return route === 'unknown' ? 'undetermined' : route;
}

/**
 * How a `.mutate(` site handles failure. With `throwOnError: false` on the
 * mutation defaults, a failure that reaches none of these routes is discarded
 * where nobody — user or maintainer — can see it.
 *
 * `readModule(fromFile, spec)` resolves a relative import to its source, or
 * returns null; a site whose hook cannot be reached is reported as
 * undetermined rather than guessed at in either direction.
 */
function classifyMutateSite(source, site, file, readModule) {
  const code = blankOutNonCode(source);
  const args = splitTopLevel(code, site.argsStart, site.argsEnd);
  if (args.length >= 2) {
    const [from, to] = args[args.length - 1];
    const brace = skipSpace(code, from);
    if (code[brace] !== '{') return 'undetermined';
    const obj = objectProperties(code, brace);
    if (obj === null || obj.close > to) return 'undetermined';
    if (obj.props.some((p) => p.name === 'onError')) return 'call-site';
    if (obj.hasSpread) return 'undetermined';
  }
  if (site.chained) return 'undetermined';

  const init = declaratorInit(code, site.receiver);
  if (init === null) return 'undetermined';
  const call = callAt(code, init);
  if (call === null) return 'undetermined';
  if (call.name === MUTATION_HOOK) return normalizeRoute(mutationCallRoute(code, call));

  const local = definitionBody(code, call.name);
  if (local !== null) return normalizeRoute(hookBodyRoute(code, local.bodyStart, local.bodyEnd));

  const imported = importedFrom(source, code, call.name);
  if (imported === null) return 'undetermined';
  const module = readModule(file, imported.spec);
  if (module === null) return 'undetermined';
  const moduleCode = blankOutNonCode(module.source);
  const def = definitionBody(moduleCode, imported.imported);
  if (def === null) return 'undetermined';
  return normalizeRoute(hookBodyRoute(moduleCode, def.bodyStart, def.bodyEnd));
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

/** Routes that leave a failed mutation with nowhere to surface. */
const SILENT_ROUTES = new Set(['none', 'hook-silent', 'undetermined']);

/**
 * Violations of the mutation rule in one file, and the route each site took,
 * so a run can report what the scanned set actually looked like.
 */
function mutateReportIn(source, label, file, readModule) {
  const code = blankOutNonCode(source);
  const tries = tryBlockSpans(code);
  const found = [];
  const routes = [];
  for (const site of findMutateCallSites(source)) {
    const route = classifyMutateSite(source, site, file, readModule);
    const inTry = tries.some(([from, to]) => site.at > from && site.at < to);
    routes.push({ route, inTry, exempt: markerOnPrecedingLine(source, site.at) !== null });
    if (!SILENT_ROUTES.has(route)) continue;
    if (markerOnPrecedingLine(source, site.at) !== null) continue;
    const line = lineNumber(source, site.at);
    const why = {
      none: `\`${site.receiver}\` comes from a mutation hook that installs no onError`,
      'hook-silent': `the onError on \`${site.receiver}\`'s hook only repairs the cache, it shows nothing`,
      undetermined: `the error path of \`${site.receiver}\` cannot be traced from this call site`,
    }[route];
    const wrapped = inTry
      ? ' The enclosing try/catch does not help: mutations run with `throwOnError: false`, so' +
        ' `.mutate()` returns without throwing and the catch never fires.'
      : '';
    found.push(
      `${label}:${line} — this .mutate() call discards its failure: ${why}, and the call site ` +
        `passes no onError.${wrapped} Await \`${site.receiver}.mutateAsync()\` inside a try/catch ` +
        `that toasts the error, pass an onError here, or explain the exception with a ` +
        `"// ${EXEMPT_MARKER} <reason>" comment on the line above.`,
    );
  }
  return { found, routes };
}

/** Reads a relative import as source, or null when it leaves the repo. */
function makeModuleReader() {
  const cache = new Map();
  return (fromFile, spec) => {
    if (!spec.startsWith('.')) return null;
    const base = resolve(dirname(fromFile), spec);
    for (const file of [
      `${base}.ts`,
      `${base}.tsx`,
      join(base, 'index.ts'),
      join(base, 'index.tsx'),
    ]) {
      if (!cache.has(file)) {
        try {
          cache.set(file, { file, source: readFileSync(file, 'utf8') });
        } catch {
          cache.set(file, null);
        }
      }
      const hit = cache.get(file);
      if (hit !== null) return hit;
    }
    return null;
  };
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

/**
 * The mutation rule applied to a sample that imports nothing, so the cases
 * exercise the analyzer rather than the repository around it.
 */
function mutateViolations(source) {
  return mutateReportIn(source, 'sample.tsx', 'sample.tsx', () => null).found;
}

/**
 * The single violation a case expects. Asserting the count first keeps a
 * detector that has stopped firing from surfacing as a type error about
 * `undefined` instead of as the missing report it is.
 */
function onlyViolation(source) {
  const found = mutateViolations(source);
  assert.equal(found.length, 1, `expected one violation, got ${found.length}`);
  return found[0];
}

/**
 * The defect, in the smallest shape that still has all of its parts: a hook
 * that installs no `onError`, called with no options argument. Every case
 * below is a substitution into this one, so a case that stops testing what it
 * claims to shows up as a broken substitution rather than a silent pass.
 */
const HOOK_WITHOUT_ON_ERROR = `
  function useSave() {
    return useMutation({
      mutationFn: async (input) => post(input),
      onSuccess: () => {},
    });
  }

  function Editor(t) {
    const save = useSave();
    const submit = () => {
      save.mutate(input);
    };
  }
`;

/**
 * A substitution into the sample above. The assertion is the point: a case
 * whose anchor text has drifted would otherwise keep testing the unmodified
 * sample and keep passing while proving nothing.
 */
function variantOf(anchor, replacement) {
  const source = HOOK_WITHOUT_ON_ERROR.replace(anchor, replacement);
  assert.notEqual(source, HOOK_WITHOUT_ON_ERROR, `the sample no longer contains "${anchor}"`);
  return source;
}

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
    [
      'reports a .mutate() whose hook installs no onError',
      () => assert.equal(mutateViolations(HOOK_WITHOUT_ON_ERROR).length, 1),
    ],
    [
      'accepts the same call once the call site passes an onError',
      () => {
        const source = variantOf(
          'save.mutate(input);',
          `save.mutate(input, {
             onError: (err) => {
               toaster.show({ tone: 'danger', message: formatApiError(err, t, 'save.error') });
             },
           });`,
        );
        assert.deepEqual(mutateViolations(source), []);
      },
    ],
    [
      'accepts a .mutate() whose hook raises a toast from onError',
      () => {
        const source = variantOf(
          'onSuccess: () => {},',
          `onError: (err) => {
             toaster.show({ tone: 'danger', message: formatApiError(err, t, 'save.error') });
           },`,
        );
        assert.deepEqual(mutateViolations(source), []);
      },
    ],
    [
      'reports a .mutate() whose hook onError only repairs the cache',
      () => {
        const source = variantOf(
          'onSuccess: () => {},',
          `onError: (_err, _vars, ctx) => {
             qc.setQueryData(key, ctx.previous);
           },`,
        );
        const message = onlyViolation(source);
        assert.match(message, /only repairs the cache/);
      },
    ],
    [
      'reads a hook whose useMutation carries explicit type arguments',
      () => {
        const source = variantOf(
          'useMutation({',
          'useMutation<void, ApiError, { id: string; body: Patch }>({',
        );
        assert.equal(mutateViolations(source).length, 1);
      },
    ],
    [
      'says the enclosing try does not catch a bare .mutate()',
      () => {
        const source = variantOf(
          'save.mutate(input);',
          `try {
             save.mutate(input);
           } catch (err) {
             toaster.show({ tone: 'danger', message: formatApiError(err, t, 'save.error') });
           }`,
        );
        const message = onlyViolation(source);
        assert.match(message, /does not help/);
        assert.match(message, /throwOnError: false/);
      },
    ],
    [
      'does not treat .mutateAsync() as a .mutate() call site',
      () =>
        assert.deepEqual(findMutateCallSites(variantOf('save.mutate(', 'save.mutateAsync(')), []),
    ],
    [
      'reports a receiver it cannot trace rather than assuming either way',
      () => {
        const source = `
          function Row({ save }) {
            const submit = () => {
              save.mutate(input);
            };
          }
        `;
        const message = onlyViolation(source);
        assert.match(message, /cannot be traced/);
      },
    ],
    [
      'honours an opt-out marker on the line above the call',
      () => {
        const source = variantOf(
          'save.mutate(input);',
          `// ${EXEMPT_MARKER} bookkeeping only, the acted-on mutation already reported\n           save.mutate(input);`,
        );
        assert.deepEqual(mutateViolations(source), []);
      },
    ],
    [
      'rejects an opt-out marker with no reason on the line above the call',
      () => {
        const source = variantOf(
          'save.mutate(input);',
          `// ${EXEMPT_MARKER}\n           save.mutate(input);`,
        );
        assert.equal(mutateViolations(source).length, 1);
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
let totalMutates = 0;

const readModule = makeModuleReader();

for (const { dir, formatters, mutateSites } of ROOTS) {
  const files = collectSourceFiles(join(repo, dir));
  let sites = 0;
  let mutates = 0;
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const label = relative(repo, file);
    sites += findErrorToastSites(source).length;
    findings.push(...violationsIn(source, label, formatters));
    const report = mutateReportIn(source, label, file, readModule);
    mutates += report.routes.length;
    findings.push(...report.found);
  }
  totalFiles += files.length;
  totalSites += sites;
  totalMutates += mutates;
  // A mistyped root would silently check nothing and pass forever.
  if (files.length === 0) empty.push(`${dir} — no .ts/.tsx sources found`);
  else if (sites === 0) empty.push(`${dir} — no error-path toast sites found`);
  else if (mutateSites && mutates === 0) empty.push(`${dir} — no .mutate() call sites found`);
}

if (empty.length > 0) {
  console.error(`check-error-toasts: ${empty.length} source root(s) yielded nothing to check:\n`);
  for (const e of empty) console.error(`  ${e}`);
  console.error(
    '\nA root that finds nothing proves nothing. Either the path in ROOTS is wrong, or the app',
  );
  console.error(
    'stopped raising the checked shapes and this root should be dropped or reconfigured.',
  );
  process.exit(1);
}

if (findings.length > 0) {
  console.error(
    `check-error-toasts: ${findings.length} failure(s) never reach the person who caused them:\n`,
  );
  for (const f of findings) console.error(`  ${f}`);
  console.error(
    '\nA fixed translated sentence hides the code and detail the API returned, and a mutation with',
  );
  console.error(
    'no error path at all hides the failure itself. Bind the error and build the message from it.',
  );
  console.error(
    'A failure with genuinely nothing to surface takes a "// error-toast-exempt: <reason>" comment.',
  );
  process.exit(1);
}

console.info(
  `check-error-toasts: every error-path toast is built from the caught error and every mutation ` +
    `has somewhere to fail (${totalSites} handler(s), ${totalMutates} .mutate() call(s), across ` +
    `${totalFiles} file(s))`,
);
