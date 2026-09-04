/**
 * check-web-bounds.ts
 *
 * Guard the length limits the web forms declare against the ones the API
 * actually enforces.
 *
 * A form field's maximum is written twice: once in the Go input struct,
 * which reaches `packages/sdk/openapi.json` as a `maxLength`, and once in
 * the Zod schema the form validates with. Nothing compares them, so the
 * two drift silently and in only one direction that matters: a frontend
 * bound *wider* than the document's promises to accept a value the server
 * will then refuse, turning an inline field error into a 422 the user
 * cannot act on. A narrower frontend bound is a deliberate choice — a
 * form may stop short of the column — and is left alone.
 *
 * The reference side is the merged OpenAPI document rather than the SQL
 * schema or the Go structs, because it is the single artefact both apps'
 * requests have to satisfy: whatever it states is what a request is
 * validated against before any handler sees it.
 *
 * Which bound in the document a form field is held to is decided in two
 * ways, and the first is preferred because it is exact.
 *
 * 1. The operation the form submits through. A form schema sits in a
 *    module that either calls the SDK itself or imports the mutation hook
 *    that does, and every such call names its method and path as
 *    literals: `client.POST('/workspaces', ...)`. That resolves to one
 *    operation in the document, whose request body bounds its properties
 *    individually. This places `name`, `title` and `description` — the
 *    most common form fields in the app, and the ones no name-based rule
 *    can place — on the value that actually applies to them.
 *
 *    A module may reach more than one operation. Nothing chooses between
 *    them: the property is compared only when every operation that bounds
 *    it states the same value, which needs no choice to be made. When
 *    they disagree the declaration is left unresolved.
 *
 * 2. The property name, for a declaration no operation was found for —
 *    a shared field builder, which by construction belongs to no single
 *    call site. A name is usable only when every `maxLength` the document
 *    states for it anywhere is the same value. Where the document states
 *    several, the name is ambiguous and the declaration is reported as
 *    unresolved rather than matched against a guessed reading; it is
 *    still held to the widest value stated anywhere, which needs no
 *    reading to be chosen, since a bound above every stated value is
 *    wider than the API under all of them.
 *
 * Declarations that reach neither is printed, not counted as clean. A
 * field nobody can place is the interesting part of the output — hiding
 * it is how a gap comes to look like coverage.
 *
 * Reading the chains:
 *
 *   - Only `z.string()` chains count, and the property name is the object
 *     key the chain is assigned to, including chains broken across lines.
 *     `Math.max(...)` is common in this codebase and can never be reached
 *     from a chain start, which the self-verification cases assert rather
 *     than assume.
 *   - A chain returned from a `create<Name>Field` builder is named after
 *     the builder, and its `.max()` argument is resolved through the
 *     local `const x = opts.maxLength ?? <literal>` default. The builders
 *     are where a shared field's real bound is written, so leaving them
 *     out would exempt the fields most likely to be right.
 *   - A chain whose key or whose `.max()` argument cannot be read is
 *     counted and reported as unnamed or unreadable. It is never assumed
 *     to be fine.
 *
 * Imports are followed one level: the module a form imports is read, and
 * the SDK calls inside the functions it imports from it are collected. A
 * hook that delegates further finds no operation and falls back to the
 * name rule rather than being resolved through a guess.
 *
 * The scanner works over the source text. It does not use the TypeScript
 * compiler API: this monorepo runs two TypeScript versions on purpose and
 * a script that imports one of them picks a side.
 *
 * The self-verification cases run before any file is read, every time.
 * This check can go quiet in more ways than it can fail — a chain form or
 * a call form that stops matching yields no declarations and no
 * operations, and both are compliant by definition — so each root has to
 * contribute files, the document has to contribute bounded names, the
 * pairing has to resolve at least one operation, and a synthetic
 * over-wide declaration is driven through the same functions the run uses
 * and must come back caught.
 *
 * Usage:
 *
 *   bun run scripts/check-web-bounds.ts
 *
 * Exit codes:
 *   0 — every comparable frontend bound is at or below the API's
 *   1 — read the input and found a wider bound, see stderr
 *   2 — could not reach a verdict: an input was unreadable or empty, or
 *       the scanner failed its own cases, so the run proves nothing and
 *       must not be read as a pass
 */

import assert from 'node:assert/strict';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '..');
const specPath = resolve(repoRoot, 'packages/sdk/openapi.json');

/** Source roots whose Zod schemas are held to the document. */
const webRoots: ReadonlyArray<string> = ['apps/flow-web/src', 'apps/accounts-web/src'];

/* ── Source text ────────────────────────────────────────────────── */

/**
 * Blank out everything that is not code — comments and the contents of
 * string, template and regex literals — replacing each character with a
 * space and keeping newlines, so offsets into the result still address
 * the original file.
 *
 * Structural scanning runs on this text. Chain traversal has to balance
 * parentheses across arguments like `.min(1, 'a)b')` and `.regex(/^)$/)`,
 * and a `.max()` never hides inside a literal, so blanking them costs
 * nothing and removes the whole class of mis-parse. A scan that needs a
 * literal's contents — an import specifier, a request path — reads the
 * original text and uses this one only to prove the match is code and
 * not a comment.
 */
export function blankNonCode(source: string): string {
  const out: string[] = [];
  let i = 0;
  // Last code character seen, which decides whether a `/` opens a regex
  // literal or divides.
  let previous = '';

  const blank = (ch: string) => {
    out.push(ch === '\n' ? '\n' : ' ');
  };

  while (i < source.length) {
    const ch = source[i] as string;
    const next = source[i + 1] ?? '';

    if (ch === '/' && next === '/') {
      while (i < source.length && source[i] !== '\n') {
        blank(source[i] as string);
        i++;
      }
      continue;
    }
    if (ch === '/' && next === '*') {
      const end = source.indexOf('*/', i + 2);
      const stop = end === -1 ? source.length : end + 2;
      while (i < stop) {
        blank(source[i] as string);
        i++;
      }
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      out.push(ch);
      i++;
      while (i < source.length) {
        const c = source[i] as string;
        if (c === '\\') {
          blank(c);
          blank(source[i + 1] ?? ' ');
          i += 2;
          continue;
        }
        if (c === ch) break;
        blank(c);
        i++;
      }
      out.push(source[i] === ch ? ch : ' ');
      i++;
      previous = ch;
      continue;
    }
    if (ch === '/' && '(,=:[!&|?{};+*%<>~^'.includes(previous)) {
      out.push(ch);
      i++;
      let inClass = false;
      while (i < source.length) {
        const c = source[i] as string;
        if (c === '\\') {
          blank(c);
          blank(source[i + 1] ?? ' ');
          i += 2;
          continue;
        }
        if (c === '[') inClass = true;
        else if (c === ']') inClass = false;
        else if (c === '\n') break;
        else if (c === '/' && !inClass) break;
        blank(c);
        i++;
      }
      out.push(source[i] === '/' ? '/' : ' ');
      i++;
      previous = '/';
      continue;
    }

    out.push(ch);
    if (!/\s/.test(ch)) previous = ch;
    i++;
  }

  return out.join('');
}

/** 1-based line number of an offset. */
function lineOf(source: string, index: number): number {
  let line = 1;
  for (let i = 0; i < index && i < source.length; i++) {
    if (source[i] === '\n') line++;
  }
  return line;
}

/**
 * Index just past the `)` matching the `(` at `open`, or -1. Runs on
 * blanked text, so nesting is the only thing to track.
 */
function matchParen(text: string, open: number): number {
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    if (text[i] === '(') depth++;
    else if (text[i] === ')') {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
}

/** Index just past the `}` matching the `{` at `open`, or -1. */
function matchBrace(text: string, open: number): number {
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    if (text[i] === '{') depth++;
    else if (text[i] === '}') {
      depth--;
      if (depth === 0) return i + 1;
    }
  }
  return -1;
}

/* ── Zod chains ─────────────────────────────────────────────────── */

/** The maximum a chain states, or why it could not be turned into one. */
type MaxState =
  | { kind: 'value'; value: number }
  | { kind: 'none' }
  | { kind: 'unreadable'; expression: string };

interface Chain {
  file: string;
  line: number;
  /** Object key or builder-derived name, null when neither could be read. */
  name: string | null;
  max: MaxState;
}

const CHAIN_START = /\bz\s*\.\s*string\s*\(\s*\)/g;
const OBJECT_KEY = /([A-Za-z_$][\w$]*)\s*:\s*$/;
const METHOD_NAME = /^[A-Za-z_$][\w$]*/;
const FIELD_BUILDER = /\bfunction\s+create([A-Za-z_$][\w$]*)Field\s*\(/g;

/**
 * Bodies of the `create<Name>Field` builders, keyed by the field name the
 * builder is named after. A chain inside one of these takes that name.
 */
function fieldBuilderRanges(text: string): Array<{ name: string; start: number; end: number }> {
  const ranges: Array<{ name: string; start: number; end: number }> = [];
  for (const m of text.matchAll(FIELD_BUILDER)) {
    // Step over the parameter list before looking for the body, or a
    // defaulted parameter (`opts: Options = {}`) supplies the brace.
    const params = matchParen(text, (m.index ?? 0) + (m[0] as string).length - 1);
    if (params === -1) continue;
    const open = text.indexOf('{', params);
    if (open === -1) continue;
    const end = matchBrace(text, open);
    if (end === -1) continue;
    ranges.push({ name: (m[1] as string).toLowerCase(), start: open, end });
  }
  return ranges;
}

/** The number a module-level `const NAME = 63` holds, if it holds one. */
function moduleConstant(text: string, identifier: string): number | null {
  const m = text.match(new RegExp(`\\bconst\\s+${identifier}\\s*(?::[^=]*)?=\\s*(\\d+)\\b`));
  return m ? Number(m[1]) : null;
}

/**
 * Resolve a `.max(<identifier>)` argument against the local default it is
 * assigned from — `const max = opts.maxLength ?? 63` — inside the builder
 * body the chain sits in. The `??` fallback is the declared default, and
 * is followed one further hop when it is a named constant, which is how
 * a limit shared with the code that generates the value is written. An
 * argument that resolves to neither stays unreadable.
 */
function resolveLocalDefault(body: string, module: string, identifier: string): number | null {
  const declaration = new RegExp(
    `\\b(?:const|let|var)\\s+${identifier}\\s*(?::[^=]*)?=\\s*([^;\\n]+)`,
  );
  const m = body.match(declaration);
  if (!m) return null;
  const expression = m[1] as string;
  const fallback = expression.match(/\?\?\s*([A-Za-z_$][\w$]*|\d+)/);
  if (fallback) {
    const value = fallback[1] as string;
    return /^\d+$/.test(value) ? Number(value) : moduleConstant(module, value);
  }
  const literal = expression.match(/^\s*(\d+)\s*$/);
  if (literal) return Number(literal[1]);
  const named = expression.match(/^\s*([A-Za-z_$][\w$]*)\s*$/);
  return named ? moduleConstant(module, named[1] as string) : null;
}

/**
 * Every `z.string()` chain in one file, with the name it is declared
 * under and the maximum it states.
 */
export function scanChains(source: string, file: string): Chain[] {
  const text = blankNonCode(source);
  const builders = fieldBuilderRanges(text);
  const chains: Chain[] = [];

  for (const m of text.matchAll(CHAIN_START)) {
    const start = m.index ?? 0;
    let cursor = start + (m[0] as string).length;

    // Walk the chain, collecting the arguments of top-level `.max()`
    // calls. Arguments are skipped as balanced blocks, so a `.max()`
    // nested inside another call's arguments belongs to that call.
    const maxArguments: string[] = [];
    for (;;) {
      while (cursor < text.length && /\s/.test(text[cursor] as string)) cursor++;
      if (text[cursor] !== '.') break;
      cursor++;
      while (cursor < text.length && /\s/.test(text[cursor] as string)) cursor++;
      const method = text.slice(cursor).match(METHOD_NAME);
      if (!method) break;
      cursor += (method[0] as string).length;
      while (cursor < text.length && /\s/.test(text[cursor] as string)) cursor++;
      if (text[cursor] !== '(') break;
      const close = matchParen(text, cursor);
      if (close === -1) break;
      if (method[0] === 'max') {
        const args = text.slice(cursor + 1, close - 1);
        maxArguments.push((args.split(',')[0] ?? '').trim());
      }
      cursor = close;
    }

    const builder = builders.find((b) => start > b.start && start < b.end);
    const key = text.slice(0, start).match(OBJECT_KEY);
    const name = key ? (key[1] as string) : (builder?.name ?? null);

    let max: MaxState = { kind: 'none' };
    for (const argument of maxArguments) {
      if (/^\d+$/.test(argument)) {
        max = { kind: 'value', value: Number(argument) };
        continue;
      }
      const resolved =
        builder && /^[A-Za-z_$][\w$]*$/.test(argument)
          ? resolveLocalDefault(text.slice(builder.start, builder.end), text, argument)
          : null;
      max =
        resolved === null
          ? { kind: 'unreadable', expression: argument }
          : { kind: 'value', value: resolved };
    }

    chains.push({ file, line: lineOf(source, start), name, max });
  }

  return chains;
}

/* ── The operation a form submits through ───────────────────────── */

interface Operation {
  method: string;
  path: string;
}

const MUTATION_CALL = /\bclient\s*\.\s*(POST|PUT|PATCH)\s*\(\s*'([^']+)'/g;
const NAMED_IMPORT = /\bimport\s+(?:type\s+)?\{([^}]*)\}\s*from\s*'([^']+)'/g;

function operationKey(operation: Operation): string {
  return `${operation.method} ${operation.path}`;
}

/**
 * Mutating SDK calls in a slice of source. The path has to be read from
 * the original text, so `blank` is consulted at the same offset to prove
 * the match is code: an example call written in a doc comment reads
 * identically and registers an operation the module never performs.
 */
function mutationCalls(source: string, blank: string, from = 0, to = source.length): Operation[] {
  const operations: Operation[] = [];
  for (const m of source.slice(from, to).matchAll(MUTATION_CALL)) {
    const at = from + (m.index ?? 0);
    if (blank.slice(at, at + 'client'.length) !== 'client') continue;
    operations.push({ method: m[1] as string, path: m[2] as string });
  }
  return operations;
}

/** Named imports from relative modules, as specifier → imported symbols. */
function relativeImports(source: string, blank: string): Map<string, string[]> {
  const imports = new Map<string, string[]>();
  for (const m of source.matchAll(NAMED_IMPORT)) {
    const at = m.index ?? 0;
    if (blank.slice(at, at + 'import'.length) !== 'import') continue;
    const specifier = m[2] as string;
    if (!specifier.startsWith('.')) continue;
    const symbols = (m[1] as string)
      .split(',')
      .map((raw) => (raw.replace(/^\s*type\s+/, '').split(/\s+as\s+/)[0] ?? '').trim())
      .filter((symbol) => /^[A-Za-z_$][\w$]*$/.test(symbol));
    imports.set(specifier, [...(imports.get(specifier) ?? []), ...symbols]);
  }
  return imports;
}

/** Body of `export function <symbol>(...)`, as offsets into `blank`. */
function exportedFunctionRange(
  blank: string,
  symbol: string,
): { start: number; end: number } | null {
  const at = blank.indexOf(`export function ${symbol}(`);
  if (at === -1) return null;
  const params = matchParen(blank, at + `export function ${symbol}`.length);
  if (params === -1) return null;
  const open = blank.indexOf('{', params);
  if (open === -1) return null;
  const end = matchBrace(blank, open);
  return end === -1 ? null : { start: open, end };
}

/**
 * Every operation the module can submit to: the SDK calls it makes
 * itself, plus those inside the functions it imports from a relative
 * module. One level, because that is where the mutation hooks live; a
 * call reached through further indirection is not resolved by guessing
 * at it.
 */
export function collectOperations(
  source: string,
  readModule: (specifier: string) => string | null,
): Operation[] {
  const blank = blankNonCode(source);
  const found = new Map<string, Operation>();
  const add = (operation: Operation) => found.set(operationKey(operation), operation);

  for (const operation of mutationCalls(source, blank)) add(operation);

  for (const [specifier, symbols] of relativeImports(source, blank)) {
    const moduleSource = readModule(specifier);
    if (moduleSource === null) continue;
    const moduleBlank = blankNonCode(moduleSource);
    for (const symbol of symbols) {
      const range = exportedFunctionRange(moduleBlank, symbol);
      if (!range) continue;
      for (const operation of mutationCalls(moduleSource, moduleBlank, range.start, range.end)) {
        add(operation);
      }
    }
  }

  return [...found.values()];
}

/* ── Document bounds ────────────────────────────────────────────── */

/**
 * Every `maxLength` the document states, keyed by wire property name.
 * Collected from every schema anywhere in the document — component
 * schemas, inline request bodies, parameter schemas — because a name
 * resolved this way has no operation to narrow it to.
 */
export function documentBounds(document: unknown): Map<string, Set<number>> {
  const bounds = new Map<string, Set<number>>();
  const seen = new Set<unknown>();

  const walk = (node: unknown): void => {
    if (!node || typeof node !== 'object') return;
    if (seen.has(node)) return;
    seen.add(node);
    if (Array.isArray(node)) {
      for (const item of node) walk(item);
      return;
    }
    const schema = node as Record<string, unknown>;
    const properties = schema.properties;
    if (properties && typeof properties === 'object' && !Array.isArray(properties)) {
      for (const [property, child] of Object.entries(properties as Record<string, unknown>)) {
        if (!child || typeof child !== 'object') continue;
        const maxLength = (child as Record<string, unknown>).maxLength;
        if (typeof maxLength !== 'number') continue;
        const values = bounds.get(property) ?? new Set<number>();
        values.add(maxLength);
        bounds.set(property, values);
      }
    }
    for (const value of Object.values(schema)) walk(value);
  };

  walk(document);
  return bounds;
}

type DocumentNode = Record<string, unknown>;

/** Follow `$ref` to the schema it names, with a cycle guard. */
function deref(
  document: DocumentNode,
  node: unknown,
  seen = new Set<string>(),
): DocumentNode | null {
  if (!node || typeof node !== 'object') return null;
  const schema = node as DocumentNode;
  const ref = schema.$ref;
  if (typeof ref !== 'string') return schema;
  const name = ref.split('/').pop() ?? '';
  if (seen.has(name)) return null;
  seen.add(name);
  const components = (document.components as DocumentNode | undefined)?.schemas as
    | Record<string, unknown>
    | undefined;
  return deref(document, components?.[name], seen);
}

/**
 * The `maxLength` each property of an operation's JSON request body is
 * given, or null when the document has no such operation or it takes no
 * JSON body.
 */
export function operationBodyBounds(
  document: unknown,
  operation: Operation,
): Map<string, number> | null {
  if (!document || typeof document !== 'object') return null;
  const root = document as DocumentNode;
  const paths = root.paths as Record<string, DocumentNode> | undefined;
  const item = paths?.[operation.path];
  const method = item?.[operation.method.toLowerCase()] as DocumentNode | undefined;
  if (!method) return null;
  const body = method.requestBody as DocumentNode | undefined;
  const content = body?.content as Record<string, DocumentNode> | undefined;
  const schema = deref(root, content?.['application/json']?.schema);
  const properties = schema?.properties;
  if (!properties || typeof properties !== 'object') return null;
  const bounds = new Map<string, number>();
  for (const [property, child] of Object.entries(properties as Record<string, unknown>)) {
    if (!child || typeof child !== 'object') continue;
    const maxLength = (child as Record<string, unknown>).maxLength;
    if (typeof maxLength === 'number') bounds.set(property, maxLength);
  }
  return bounds;
}

/* ── Comparison ─────────────────────────────────────────────────── */

/** A chain together with the operations its module can submit to. */
interface Declaration {
  chain: Chain;
  operations: ReadonlyArray<Operation>;
}

interface Violation {
  chain: Chain;
  declared: number;
  stated: number;
  /** How the stated bound was reached, for the message. */
  via: string;
}

interface Unresolved {
  chain: Chain;
  reason: string;
}

interface Verdict {
  violations: Violation[];
  unresolved: Unresolved[];
  /** Chains placed on an operation's request body property. */
  byOperation: number;
  /** Chains placed on a property name the document bounds unambiguously. */
  byName: number;
  /** Chains that state no maximum at all. */
  unbounded: number;
}

export function compare(
  declarations: ReadonlyArray<Declaration>,
  nameBounds: Map<string, Set<number>>,
  bodyBounds: (operation: Operation) => Map<string, number> | null,
): Verdict {
  const violations: Violation[] = [];
  const unresolved: Unresolved[] = [];
  let byOperation = 0;
  let byName = 0;
  let unbounded = 0;

  for (const { chain, operations } of declarations) {
    if (chain.max.kind === 'none') {
      unbounded++;
      continue;
    }
    if (chain.max.kind === 'unreadable') {
      unresolved.push({
        chain,
        reason: `states .max(${chain.max.expression}), which this scanner cannot resolve to a number`,
      });
      continue;
    }
    const declared = chain.max.value;
    if (chain.name === null) {
      unresolved.push({
        chain,
        reason: `states .max(${declared}) but is not assigned to a property name`,
      });
      continue;
    }

    // The operation the form submits through, when every operation the
    // module reaches agrees on this property.
    const stated = new Map<string, number>();
    for (const operation of operations) {
      const value = bodyBounds(operation)?.get(chain.name);
      if (value !== undefined) stated.set(operationKey(operation), value);
    }
    const distinct = new Set(stated.values());
    if (distinct.size === 1) {
      byOperation++;
      const only = [...distinct][0] as number;
      const where = [...stated.keys()].join(', ');
      if (declared > only) {
        violations.push({ chain, declared, stated: only, via: `${where} bounds it at ${only}` });
      }
      continue;
    }
    if (distinct.size > 1) {
      const spread = [...stated].map(([op, value]) => `${op}=${value}`).join(', ');
      unresolved.push({
        chain,
        reason: `the operations this module reaches bound this name differently (${spread})`,
      });
      continue;
    }

    // No operation bounds it: fall back to the name across the document.
    const values = [...(nameBounds.get(chain.name) ?? [])].sort((a, b) => a - b);
    if (values.length === 0) {
      unresolved.push({
        chain,
        reason:
          operations.length === 0
            ? 'no operation was found for this module and the document bounds no property of this name'
            : 'the operations this module reaches do not bound this name, and neither does the document under it',
      });
      continue;
    }
    if (values.length === 1) {
      byName++;
      const only = values[0] as number;
      if (declared > only) {
        violations.push({
          chain,
          declared,
          stated: only,
          via: `the document bounds every \`${chain.name}\` at ${only}`,
        });
      }
      continue;
    }
    // Ambiguous name with no operation to narrow it: no single reading to
    // compare against, but a bound above every stated value is wider than
    // the API under all of them.
    const widest = values[values.length - 1] as number;
    unresolved.push({
      chain,
      reason: `no operation bounds it and the document states ${values.length} values for this name (${values.join(', ')})`,
    });
    if (declared > widest) {
      violations.push({
        chain,
        declared,
        stated: widest,
        via: `wider than every \`${chain.name}\` the document bounds (widest ${widest})`,
      });
    }
  }

  return { violations, unresolved, byOperation, byName, unbounded };
}

/* ── File discovery ─────────────────────────────────────────────── */

/** Source files under a root, excluding tests. */
function sourceFiles(root: string): string[] {
  const files: string[] = [];
  const walk = (dir: string): void => {
    let entries: ReturnType<typeof readdirSync>;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const full = resolve(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === '__tests__' || entry.name === 'node_modules') continue;
        walk(full);
        continue;
      }
      if (!/\.tsx?$/.test(entry.name)) continue;
      if (/\.test\.tsx?$/.test(entry.name)) continue;
      files.push(full);
    }
  };
  walk(root);
  return files.sort();
}

/** Read a relative import as TypeScript source, or null if it is not one. */
function readRelativeModule(from: string, specifier: string): string | null {
  const base = resolve(dirname(from), specifier);
  for (const candidate of [
    `${base}.ts`,
    `${base}.tsx`,
    resolve(base, 'index.ts'),
    resolve(base, 'index.tsx'),
  ]) {
    if (existsSync(candidate)) return readFileSync(candidate, 'utf8');
  }
  return null;
}

/* ── Self-verification ──────────────────────────────────────────── */

function selfCheck(): string[] {
  const FORM = [
    "import { z } from 'zod';",
    "import { useCreateWorkspace, useRenameWorkspace } from './api';",
    '',
    '// A commented chain must not count: slug: z.string().max(4096)',
    'const schema = z.object({',
    "  title: z.string().min(1, 'form.title').max(500),",
    '  slug: z',
    '    .string()',
    "    .min(1, 'form.slug)required')",
    '    .max(9999)',
    '    .regex(/^[a-z0-9-]+$/, "form.slug"),',
    "  note: z.string().max(10).or(z.literal('')),",
    '  free: z.string().min(1),',
    '});',
    '',
    'function widest(a: number, b: number): number {',
    '  return Math.max(a, b, ...[1, 2, 3]);',
    '}',
    '',
  ].join('\n');

  const API = [
    'export function useCreateWorkspace() {',
    '  return useMutation({',
    "    // Superseded, kept for the changelog: client.POST('/legacy/workspaces', {})",
    '    mutationFn: async (input) =>',
    "      apiRequest((client) => client.POST('/workspaces', { body: input })),",
    '  });',
    '}',
    '',
    'export function useRenameWorkspace() {',
    "  return useMutation({ mutationFn: () => client.PATCH('/workspaces/{wsId}', {}) });",
    '}',
    '',
  ].join('\n');

  const LEGACY_API = [
    'export function useCreateWorkspace() {',
    "  return useMutation({ mutationFn: () => client.POST('/workspaces', {}) });",
    '}',
    '',
    'export function useLegacyCreateWorkspace() {',
    "  return useMutation({ mutationFn: () => client.POST('/legacy/workspaces', {}) });",
    '}',
    '',
  ].join('\n');

  const BUILDER = [
    "import { z } from 'zod';",
    '',
    'export const NOTE_MAX_LENGTH = 10;',
    '',
    'export function createNoteField(opts: NoteFieldOptions): z.ZodString {',
    '  const max = opts.maxLength ?? NOTE_MAX_LENGTH;',
    '  return z',
    '    .string()',
    '    .min(1, opts.requiredKey)',
    '    .max(max)',
    '    .regex(/^[a-z0-9-]+$/, opts.formatKey);',
    '}',
    '',
    'export function createIdentifierField(opts: IdentifierFieldOptions = {}): z.ZodString {',
    '  const max = opts.maxLength ?? 5;',
    '  return z.string().max(max);',
    '}',
    '',
  ].join('\n');

  const readApi = (specifier: string) => (specifier === './api' ? API : null);

  /**
   * A document-shaped graph with one operation bounding the names the
   * form declares, a sibling operation bounding one of them at another
   * value, one name bounded once across the whole document, and one
   * bounded at several values. The clean cases are as load-bearing as the
   * wide one: a rule that fired on a narrower bound, or that picked a
   * reading for an ambiguous name, would need exemptions, and an
   * exemption list is how a guard stops meaning anything.
   */
  const DOCUMENT = {
    paths: {
      '/workspaces': {
        post: {
          requestBody: {
            content: {
              'application/json': { schema: { $ref: '#/components/schemas/createWorkspace' } },
            },
          },
        },
      },
      '/workspaces/{wsId}': {
        patch: {
          requestBody: {
            content: {
              'application/json': {
                schema: { properties: { title: { maxLength: 500 }, slug: { maxLength: 63 } } },
              },
            },
          },
        },
      },
      '/legacy/workspaces': {
        post: {
          requestBody: {
            content: {
              'application/json': { schema: { properties: { slug: { maxLength: 99999 } } } },
            },
          },
        },
      },
    },
    components: {
      schemas: {
        createWorkspace: {
          properties: {
            title: { maxLength: 500 },
            slug: { maxLength: 63 },
            note: { maxLength: 1000 },
          },
        },
        other: { properties: { title: { maxLength: 200 }, identifier: { maxLength: 5 } } },
      },
    },
  };

  const NAME_BOUNDS = documentBounds(DOCUMENT);
  const bodyBounds = (operation: Operation) => operationBodyBounds(DOCUMENT, operation);
  const declare = (
    source: string,
    file: string,
    read: (s: string) => string | null = () => null,
  ) => {
    const operations = collectOperations(source, read);
    return scanChains(source, file).map((chain) => ({ chain, operations }));
  };

  const cases: ReadonlyArray<[string, () => void]> = [
    [
      'catches a bound wider than the operation the form submits through',
      () => {
        const { violations, byOperation } = compare(
          declare(FORM, 'form.tsx', readApi),
          NAME_BOUNDS,
          bodyBounds,
        );
        assert.equal(byOperation, 3);
        assert.deepEqual(
          violations.map((v) => [v.chain.name, v.declared, v.stated]),
          [['slug', 9999, 63]],
        );
      },
    ],
    [
      'follows a form to the operation its imported hook calls, and ignores one written in a comment',
      () => {
        assert.deepEqual(collectOperations(FORM, readApi), [
          { method: 'POST', path: '/workspaces' },
          { method: 'PATCH', path: '/workspaces/{wsId}' },
        ]);
      },
    ],
    [
      'reads a name off a chain broken across lines and past a string holding a paren',
      () => {
        const slug = scanChains(FORM, 'form.tsx').find((c) => c.name === 'slug');
        assert.equal(slug?.max.kind === 'value' ? slug.max.value : null, 9999);
        assert.equal(slug?.line, 7);
      },
    ],
    [
      'never reaches a Math.max, a commented chain, or an unrelated call',
      () => {
        assert.deepEqual(
          scanChains(FORM, 'form.tsx').map((c) => c.name),
          ['title', 'slug', 'note', 'free'],
        );
        assert.deepEqual(scanChains('const n = Math.max(a, b);\n', 'x.ts'), []);
      },
    ],
    [
      'leaves a narrower bound and a chain with no maximum alone',
      () => {
        const { violations, unbounded } = compare(
          declare(FORM, 'form.tsx', readApi),
          NAME_BOUNDS,
          bodyBounds,
        );
        assert.equal(
          violations.some((v) => v.chain.name === 'note' || v.chain.name === 'free'),
          false,
        );
        assert.equal(unbounded, 1);
      },
    ],
    [
      'compares a property both reachable operations agree on without choosing between them',
      () => {
        const title = compare(declare(FORM, 'form.tsx', readApi), NAME_BOUNDS, bodyBounds);
        assert.deepEqual(title.unresolved, []);
      },
    ],
    [
      'refuses to choose when the reachable operations disagree',
      () => {
        const source = [
          "import { useCreateWorkspace, useLegacyCreateWorkspace } from './api';",
          'const schema = z.object({ slug: z.string().max(9999) });',
        ].join('\n');
        const { unresolved, byOperation } = compare(
          declare(source, 'form.tsx', (s) => (s === './api' ? LEGACY_API : null)),
          NAME_BOUNDS,
          bodyBounds,
        );
        assert.equal(byOperation, 0);
        assert.match(unresolved[0]?.reason ?? '', /bound this name differently/);
      },
    ],
    [
      'falls back to the property name for a builder no operation reaches',
      () => {
        const declarations = declare(BUILDER, 'identifier.ts');
        assert.deepEqual(
          declarations.map((d) => [
            d.chain.name,
            d.chain.max.kind === 'value' ? d.chain.max.value : d.chain.max.kind,
          ]),
          [
            ['note', 10],
            ['identifier', 5],
          ],
        );
        const { violations, byName, byOperation } = compare(declarations, NAME_BOUNDS, bodyBounds);
        assert.equal(byOperation, 0);
        assert.equal(byName, 2);
        assert.deepEqual(violations, []);
      },
    ],
    [
      'reports an ambiguous name with no operation, and still catches a bound above every value it takes',
      () => {
        const wide = declare('const s = z.object({ title: z.string().max(600) });\n', 'w.ts');
        const { violations, unresolved, byName } = compare(wide, NAME_BOUNDS, bodyBounds);
        assert.equal(byName, 0);
        assert.equal(unresolved.length, 1);
        assert.deepEqual(
          violations.map((v) => [v.declared, v.stated]),
          [[600, 500]],
        );
      },
    ],
    [
      'reports a maximum it cannot resolve instead of skipping the chain',
      () => {
        const chains = declare('const s = z.object({ slug: z.string().max(LIMIT) });\n', 'u.ts');
        const { unresolved, byOperation, byName } = compare(chains, NAME_BOUNDS, bodyBounds);
        assert.equal(byOperation + byName, 0);
        assert.match(unresolved[0]?.reason ?? '', /cannot resolve/);
      },
    ],
    [
      'reads maxLength off nested and inline schemas alike',
      () => {
        const bounds = documentBounds({
          components: {
            schemas: {
              workspace: { properties: { slug: { type: 'string', maxLength: 63 } } },
              page: {
                properties: { items: { items: { properties: { slug: { maxLength: 32 } } } } },
              },
            },
          },
          paths: {
            '/x': {
              post: {
                requestBody: {
                  content: {
                    'application/json': { schema: { properties: { code: { maxLength: 6 } } } },
                  },
                },
              },
            },
          },
        });
        assert.deepEqual(
          [...(bounds.get('slug') ?? [])].sort((a, b) => a - b),
          [32, 63],
        );
        assert.deepEqual([...(bounds.get('code') ?? [])], [6]);
        assert.equal(bounds.has('items'), false);
      },
    ],
  ];

  const failures: string[] = [];
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
    `check-web-bounds: ${selfFailures.length} self-verification case(s) failed, so no form was read:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the forms.',
  );
  process.exit(2);
}

/* ── Run ────────────────────────────────────────────────────────── */

const blockers: string[] = [];

let document: unknown;
try {
  document = JSON.parse(readFileSync(specPath, 'utf8'));
} catch (err) {
  console.error(
    `check-web-bounds: could not read the API document at ${specPath}: ${
      err instanceof Error ? err.message : String(err)
    }`,
  );
  process.exit(2);
}

const nameBounds = documentBounds(document);
if (nameBounds.size === 0) {
  blockers.push(
    `the API document at ${specPath} states no maxLength on any property, so there is nothing to compare against`,
  );
}

const declarations: Declaration[] = [];
const chainFiles = new Set<string>();
let chainCount = 0;
let pairedFiles = 0;
for (const root of webRoots) {
  const files = sourceFiles(resolve(repoRoot, root));
  if (files.length === 0) {
    blockers.push(`${root} contributed no source files — the scan covered none of that app`);
    continue;
  }
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const chains = scanChains(source, relative(repoRoot, file));
    if (chains.length === 0) continue;
    chainFiles.add(file);
    chainCount += chains.length;
    // Import resolution costs a read per relative import, so it runs only
    // for the modules that declare a bound at all.
    const operations = chains.some((c) => c.max.kind === 'value')
      ? collectOperations(source, (specifier) => readRelativeModule(file, specifier))
      : [];
    if (operations.length > 0) pairedFiles++;
    for (const chain of chains) declarations.push({ chain, operations });
  }
}

if (chainCount === 0) {
  blockers.push(
    'no z.string() chain was found in either app — the chain form no longer matches the source, and an empty scan passes every comparison that reads it',
  );
}
if (pairedFiles === 0) {
  blockers.push(
    'no module that declares a bound resolved to an SDK operation — the call form no longer matches the source, and every declaration would silently drop to the weaker property-name rule',
  );
}

const { violations, unresolved, byOperation, byName, unbounded } = compare(
  declarations,
  nameBounds,
  (operation) => operationBodyBounds(document, operation),
);

if (blockers.length > 0) {
  console.error('check-web-bounds: could not reach a verdict, so this run proves nothing:');
  for (const b of blockers) console.error(`  - ${b}`);
  process.exit(2);
}

if (unresolved.length > 0) {
  console.info(
    `check-web-bounds: ${unresolved.length} declaration(s) the document cannot place. These are unchecked, not clean:`,
  );
  for (const u of unresolved) {
    const declared = u.chain.max.kind === 'value' ? `.max(${u.chain.max.value})` : 'a maximum';
    console.info(
      `  - ${u.chain.file}:${u.chain.line} ${u.chain.name ?? '<unnamed>'} declares ${declared} — ${u.reason}`,
    );
  }
}

if (violations.length > 0) {
  console.error(
    '\ncheck-web-bounds: form field(s) accept more than the API does. The form validates, the request is refused, and the user sees a 422 where an inline error belongs — narrow the Zod bound, or widen the API deliberately:',
  );
  for (const v of violations) {
    console.error(`  - ${v.chain.file}:${v.chain.line} declares .max(${v.declared}) — ${v.via}`);
  }
  process.exit(1);
}

console.info(
  `check-web-bounds: ${chainCount} chain(s) across ${chainFiles.size} file(s); ${byOperation + byName} compared against the API document (${byOperation} through the operation the form submits to, ${byName} by property name), ${unresolved.length} unresolved; ${unbounded} declare no maximum; ${nameBounds.size} bounded property name(s) in the document`,
);
