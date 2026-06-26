/**
 * check-inline-spacing
 *
 * Scans .tsx / .ts source files for inline `style={{ ... }}` declarations
 * (and CSS rule bodies) that use hardcoded spacing / sizing literals
 * instead of the `--nf-space-*` / `--nf-radius-*` / `--nf-text-*` design
 * tokens.
 *
 * The intent is to enforce rule #19 of CLAUDE.md / docs/conventions/design-tokens.md:
 * spacing, sizing, radii, and font-size must flow through CSS custom
 * properties rather than literal `0.75rem` / `12px` values.
 *
 * Detected properties (both CSS-kebab and JSX-camelCase variants):
 *
 *   Spacing : gap, columnGap, rowGap, padding, paddingTop, paddingInline,
 *             margin, marginInline, marginBlockEnd, ...
 *   Position: inset, top, left, right, bottom, insetInline*, insetBlock*
 *   Sizing  : width, height, min-/maxWidth, min-/maxHeight,
 *             minInlineSize, maxInlineSize, minBlockSize, maxBlockSize
 *   Radius  : borderRadius (and *Radius corners)
 *   Type    : fontSize
 *
 * Detected values:
 *
 *   - `<number>rem`          (e.g. `0.375rem`, `1.5rem`)
 *   - `<integer>px` where N >= 4 (covers borders larger than the hairline,
 *     icon sizes, fixed dimensions). 0..3 px values for borders are
 *     allowed because the design system explicitly carves out the
 *     1px hairline rule.
 *
 * Allow-list:
 *
 *   - Files containing the `nf-token-override` annotation anywhere in the
 *     body (same convention used by check-hardcoded-colors.sh and the
 *     source-colors files).
 *   - Theme / token CSS sources under `packages/ui/src/themes/**` and
 *     `packages/ui/src/tokens/**`.
 *   - Application stylesheet roots under `apps/<app>/src/styles/**`.
 *   - Test files (`*.test.*`, `*.spec.*`).
 *
 * CLI:
 *
 *   tsx scripts/check-inline-spacing.ts            # report only, exit 0
 *   tsx scripts/check-inline-spacing.ts --ci       # exit 1 on offenses
 *   tsx scripts/check-inline-spacing.ts --json     # machine-readable output
 *
 * The scanner intentionally avoids regular expressions (per project
 * convention; see check-theme-parity.ts) and walks each file character
 * by character. This keeps the parse predictable and easy to audit.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

/** A single offense flagged by the scanner. */
export interface SpacingOffense {
  /** Absolute path of the file. */
  file: string;
  /** 1-based line number of the offense. */
  line: number;
  /** 1-based column where the value starts. */
  column: number;
  /** The CSS property (canonical kebab form, e.g. `gap`, `min-inline-size`). */
  property: string;
  /** The literal value flagged (e.g. `0.375rem`, `12px`). */
  value: string;
  /** The full source line for context. */
  context: string;
}

/** Scanner configuration. */
export interface ScanOptions {
  /** Project root (defaults to repo root). */
  root: string;
  /** Directory globs (relative to root) to scan recursively. */
  scanDirs: ReadonlyArray<string>;
  /** Path fragments that exclude a file from the scan. */
  excludeFragments: ReadonlyArray<string>;
}

/**
 * Properties whose numeric values must come from a token. The scanner
 * accepts both kebab-case (e.g. inside template strings or CSS source)
 * and camelCase (JSX `style={{}}` keys). The set below stores the
 * canonical kebab-case identifier used in offense reports.
 */
const TOKENED_PROPERTIES: ReadonlyArray<string> = [
  // spacing
  'gap',
  'column-gap',
  'row-gap',
  'padding',
  'padding-top',
  'padding-right',
  'padding-bottom',
  'padding-left',
  'padding-inline',
  'padding-inline-start',
  'padding-inline-end',
  'padding-block',
  'padding-block-start',
  'padding-block-end',
  'margin',
  'margin-top',
  'margin-right',
  'margin-bottom',
  'margin-left',
  'margin-inline',
  'margin-inline-start',
  'margin-inline-end',
  'margin-block',
  'margin-block-start',
  'margin-block-end',
  // position
  'inset',
  'top',
  'left',
  'right',
  'bottom',
  'inset-inline',
  'inset-inline-start',
  'inset-inline-end',
  'inset-block',
  'inset-block-start',
  'inset-block-end',
  // sizing
  'width',
  'height',
  'min-width',
  'max-width',
  'min-height',
  'max-height',
  'min-inline-size',
  'max-inline-size',
  'min-block-size',
  'max-block-size',
  'block-size',
  'inline-size',
  // radius
  'border-radius',
  'border-top-left-radius',
  'border-top-right-radius',
  'border-bottom-left-radius',
  'border-bottom-right-radius',
  'border-start-start-radius',
  'border-start-end-radius',
  'border-end-start-radius',
  'border-end-end-radius',
  // typography
  'font-size',
];

/**
 * camelCase ↔ kebab-case conversion table built from `TOKENED_PROPERTIES`.
 * The scanner detects both forms because JSX inline styles use camelCase
 * keys while CSS / tagged template strings use kebab-case.
 */
const PROPERTY_LOOKUP: ReadonlyMap<string, string> = (() => {
  const m = new Map<string, string>();
  for (const kebab of TOKENED_PROPERTIES) {
    m.set(kebab, kebab);
    const camel = kebab.replace(/-([a-z])/g, (_match, c: string) => c.toUpperCase());
    m.set(camel, kebab);
  }
  return m;
})();

/** Px values strictly below this threshold are tolerated (covers 1px hairlines). */
const PX_THRESHOLD = 4;

/**
 * Returns true when `path` (or one of its ancestors up to `root`) carries
 * the `nf-token-override` annotation. The annotation may appear anywhere
 * in the file body and applies to the whole file.
 */
function hasOverride(path: string): boolean {
  try {
    const src = readFileSync(path, 'utf8');
    return src.includes('nf-token-override');
  } catch {
    return false;
  }
}

/**
 * Returns true when `c` is a character that may legitimately appear inside
 * a CSS / JS identifier (ASCII subset is sufficient for our property names).
 */
function isIdentChar(c: string): boolean {
  return (
    (c >= 'a' && c <= 'z') ||
    (c >= 'A' && c <= 'Z') ||
    (c >= '0' && c <= '9') ||
    c === '-' ||
    c === '_'
  );
}

/**
 * Walk `text` and collect property/value pairs that match a tokened
 * property and a numeric literal. The walker recognises the two shapes
 * relevant to this codebase:
 *
 *   1. JSX inline style — `gap: '0.375rem'` / `gap: "0.375rem"`
 *   2. CSS / tagged template — `gap: 0.375rem;` / `gap:0.375rem`
 *
 * It does NOT attempt to be a full CSS or JS parser; the heuristic is to
 * find an identifier, skip whitespace, see a colon, then locate the next
 * numeric literal (with optional surrounding quotes) before the next
 * comma / semicolon / closing brace.
 */
function scanText(file: string, text: string): SpacingOffense[] {
  const out: SpacingOffense[] = [];
  const len = text.length;

  // Pre-compute line offsets for fast position → (line, column) mapping.
  const lineStarts: number[] = [0];
  for (let i = 0; i < len; i += 1) {
    if (text[i] === '\n') lineStarts.push(i + 1);
  }
  const positionToLineCol = (pos: number): { line: number; column: number } => {
    // binary search for the largest lineStart <= pos
    let lo = 0;
    let hi = lineStarts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >>> 1;
      const start = lineStarts[mid];
      if (start !== undefined && start <= pos) {
        lo = mid;
      } else {
        hi = mid - 1;
      }
    }
    const start = lineStarts[lo] ?? 0;
    return { line: lo + 1, column: pos - start + 1 };
  };
  const lineAt = (lineIdx: number): string => {
    const start = lineStarts[lineIdx - 1];
    if (start === undefined) return '';
    const next = lineStarts[lineIdx] ?? len + 1;
    const endExclusive = next > 0 ? next - 1 : start;
    return text.slice(start, Math.max(start, endExclusive));
  };

  let i = 0;
  while (i < len) {
    const ch = text[i];
    if (ch === undefined) break;

    // Skip JS-style and CSS-style comments so commented-out code does not
    // produce false positives.
    if (ch === '/' && text[i + 1] === '/') {
      while (i < len && text[i] !== '\n') i += 1;
      continue;
    }
    if (ch === '/' && text[i + 1] === '*') {
      i += 2;
      while (i < len && !(text[i] === '*' && text[i + 1] === '/')) i += 1;
      i += 2;
      continue;
    }

    if (!isIdentChar(ch)) {
      i += 1;
      continue;
    }

    // Identifier must start at a non-ident boundary (i.e. previous char
    // is not part of a longer identifier). This avoids matching the tail
    // of `padding` inside `paddingTop` for example.
    const prev = i > 0 ? text[i - 1] : undefined;
    if (prev !== undefined && isIdentChar(prev)) {
      i += 1;
      continue;
    }

    // Read the identifier.
    let j = i;
    while (j < len) {
      const cj = text[j];
      if (cj === undefined || !isIdentChar(cj)) break;
      j += 1;
    }
    const ident = text.slice(i, j);

    // Look up canonical kebab form; skip non-tokened properties.
    const kebab = PROPERTY_LOOKUP.get(ident);
    if (kebab === undefined) {
      i = j;
      continue;
    }

    // Skip whitespace then expect a colon.
    let k = j;
    while (k < len && (text[k] === ' ' || text[k] === '\t')) k += 1;
    if (text[k] !== ':') {
      i = j;
      continue;
    }
    k += 1;

    // Collect the value up to the next `;`, `,`, `}`, `\n`, or EOF.
    // We then look for a numeric literal inside it.
    let valEnd = k;
    let depth = 0;
    while (valEnd < len) {
      const cv = text[valEnd];
      if (cv === undefined) break;
      if (cv === '(' || cv === '{' || cv === '[') depth += 1;
      else if (cv === ')' || cv === '}' || cv === ']') {
        if (depth === 0) break;
        depth -= 1;
      } else if (depth === 0 && (cv === ';' || cv === ',' || cv === '\n')) {
        break;
      }
      valEnd += 1;
    }
    const valueChunk = text.slice(k, valEnd);

    // Skip values that already use a token reference. `var(--nf-...)` is
    // the canonical form; we also accept `calc()` expressions whose
    // operands are token references.
    if (valueChunk.includes('var(--nf-')) {
      i = valEnd;
      continue;
    }
    // Skip CSS keywords / sentinel values.
    const trimmed = valueChunk.trim().replace(/^['"]/, '').replace(/['"]$/, '');
    if (
      trimmed === '' ||
      trimmed === '0' ||
      trimmed === 'auto' ||
      trimmed === 'inherit' ||
      trimmed === 'unset' ||
      trimmed === 'initial' ||
      trimmed === 'none' ||
      trimmed === 'revert' ||
      trimmed === 'fit-content' ||
      trimmed === 'max-content' ||
      trimmed === 'min-content' ||
      trimmed === '100%' ||
      trimmed === '50%' ||
      trimmed === '0%'
    ) {
      i = valEnd;
      continue;
    }

    // Walk the value looking for `<digits>(.<digits>)?(rem|px)` literals.
    // Literals that are part of `var(...)` expressions are already
    // skipped above.
    let p = 0;
    const vlen = valueChunk.length;
    while (p < vlen) {
      const cp = valueChunk[p];
      if (cp === undefined) break;
      if (cp >= '0' && cp <= '9') {
        // Read number.
        let q = p;
        while (q < vlen) {
          const cq = valueChunk[q];
          if (cq === undefined) break;
          if (!((cq >= '0' && cq <= '9') || cq === '.')) break;
          q += 1;
        }
        const numText = valueChunk.slice(p, q);
        // Read unit (rem | px | em | other identifier).
        let r = q;
        while (r < vlen) {
          const cr = valueChunk[r];
          if (cr === undefined) break;
          if (!((cr >= 'a' && cr <= 'z') || (cr >= 'A' && cr <= 'Z'))) break;
          r += 1;
        }
        const unit = valueChunk.slice(q, r);
        const numValue = Number(numText);
        let flag = false;
        if (unit === 'rem') {
          // All hardcoded rem values are flagged (spacing tokens cover the
          // standard scale; tests for arbitrary `0.375rem` etc. should be
          // mapped or added to the scale).
          if (Number.isFinite(numValue) && numValue !== 0) flag = true;
        } else if (unit === 'px') {
          if (Number.isFinite(numValue) && numValue >= PX_THRESHOLD) flag = true;
        }
        if (flag) {
          const absPos = k + p;
          const { line, column } = positionToLineCol(absPos);
          out.push({
            file,
            line,
            column,
            property: kebab,
            value: `${numText}${unit}`,
            context: lineAt(line).trim(),
          });
        }
        p = r;
        continue;
      }
      p += 1;
    }

    i = valEnd;
  }

  return out;
}

/**
 * Recursively collect candidate files under `dir`. Honours the exclusion
 * fragments so we never descend into `node_modules`, `dist`, etc.
 */
function collectFiles(dir: string, exclude: ReadonlyArray<string>, acc: string[]): void {
  let entries: string[];
  try {
    // Use the string-name overload explicitly so TypeScript does not pick
    // the Dirent[] return type. We only need the leaf names here.
    entries = readdirSync(dir, { encoding: 'utf8' }) as string[];
  } catch {
    return;
  }
  for (const name of entries) {
    const full = join(dir, name);
    const skip = exclude.some((frag) => full.includes(frag));
    if (skip) continue;
    let st: ReturnType<typeof statSync>;
    try {
      st = statSync(full);
    } catch {
      continue;
    }
    if (st.isDirectory()) {
      collectFiles(full, exclude, acc);
      continue;
    }
    if (!st.isFile()) continue;
    if (name.endsWith('.tsx') || name.endsWith('.ts') || name.endsWith('.css')) {
      // Skip declaration files and module css typings.
      if (name.endsWith('.d.ts')) continue;
      if (name.endsWith('.module.css.d.ts')) continue;
      acc.push(full);
    }
  }
}

/**
 * Run the scanner across `options.scanDirs` and return all offenses.
 * Exposed for tests so we can feed synthetic inputs without touching the
 * real file system.
 */
export function scanFiles(options: ScanOptions): SpacingOffense[] {
  const files: string[] = [];
  for (const rel of options.scanDirs) {
    collectFiles(resolve(options.root, rel), options.excludeFragments, files);
  }
  const out: SpacingOffense[] = [];
  for (const file of files) {
    if (hasOverride(file)) continue;
    let src: string;
    try {
      src = readFileSync(file, 'utf8');
    } catch {
      continue;
    }
    out.push(...scanText(file, src));
  }
  return out;
}

/**
 * Public test entry: scan a single in-memory source string. Used by the
 * unit tests to feed synthetic inputs.
 */
export function scanSource(file: string, source: string): SpacingOffense[] {
  if (source.includes('nf-token-override')) return [];
  return scanText(file, source);
}

const DEFAULT_SCAN_DIRS: ReadonlyArray<string> = [
  'apps/flow-web/src',
  'apps/accounts-web/src',
  'packages/ui/src/primitives',
  'packages/ui/src/calendar',
];

const DEFAULT_EXCLUDE_FRAGMENTS: ReadonlyArray<string> = [
  '/node_modules/',
  '/dist/',
  '/.git/',
  '/packages/ui/src/themes/',
  '/packages/ui/src/tokens/',
  '/apps/flow-web/src/styles/',
  '/apps/accounts-web/src/styles/',
  '.test.',
  '.spec.',
];

interface CliFlags {
  ci: boolean;
  json: boolean;
  root: string;
}

function parseFlags(argv: ReadonlyArray<string>): CliFlags {
  let ci = false;
  let json = false;
  let root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--ci') ci = true;
    else if (a === '--json') json = true;
    else if (a === '--root' && i + 1 < argv.length) {
      const next = argv[i + 1];
      if (next !== undefined) {
        root = resolve(next);
        i += 1;
      }
    }
  }
  return { ci, json, root };
}

function main(): void {
  const flags = parseFlags(process.argv.slice(2));
  const offenses = scanFiles({
    root: flags.root,
    scanDirs: DEFAULT_SCAN_DIRS,
    excludeFragments: DEFAULT_EXCLUDE_FRAGMENTS,
  });

  if (flags.json) {
    process.stdout.write(JSON.stringify(offenses, null, 2));
    process.stdout.write('\n');
  } else if (offenses.length === 0) {
    console.info('check-inline-spacing: OK (no hardcoded spacing literals)');
  } else {
    console.error(`check-inline-spacing: found ${offenses.length} offense(s)`);
    // Group offenses by file for readable output.
    const byFile = new Map<string, SpacingOffense[]>();
    for (const o of offenses) {
      const list = byFile.get(o.file) ?? [];
      list.push(o);
      byFile.set(o.file, list);
    }
    const files = Array.from(byFile.keys()).sort();
    for (const file of files) {
      const list = byFile.get(file) ?? [];
      console.error(`\n  ${relative(flags.root, file)} (${list.length})`);
      for (const o of list) {
        console.error(`    ${o.line}:${o.column}  ${o.property}: ${o.value}`);
      }
    }
    console.error(
      '\n  Replace literals with var(--nf-space-*), var(--nf-radius-*), or var(--nf-text-*).',
    );
    console.error(
      '  Files that legitimately need a literal (e.g. external integration constants) may add an',
    );
    console.error('  `nf-token-override: <reason>` annotation to opt out.');
  }

  if (flags.ci && offenses.length > 0) {
    process.exit(1);
  }
}

// Only run as CLI when invoked directly (not when imported by tests).
const invokedDirectly = (() => {
  try {
    const here = fileURLToPath(import.meta.url);
    const arg1 = process.argv[1];
    if (typeof arg1 !== 'string') return false;
    return resolve(arg1) === here;
  } catch {
    return false;
  }
})();
if (invokedDirectly) main();
