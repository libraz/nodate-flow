import {
  existsSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { parse } from '@babel/parser';
import type { CallExpression, Node } from '@babel/types';
import { describe, expect, it } from 'vitest';

/**
 * Holds zone resolution to a single place across the TypeScript tree.
 *
 * The counterpart of `TestZoneResolutionIsCentralised` in
 * `packages/go-shared/region`, and it exists for the same reason: the
 * failure this prevents is invisible in review and invisible in a test
 * run on one machine. Code that reads the host timezone, or that parses
 * a timestamp without saying which zone to read it in, produces the
 * right answer for whoever wrote it and a different answer for a reader
 * a few hours east. A calendar built that way disagrees with the server
 * about which day an event is on, and nothing fails until somebody
 * travels.
 *
 * Two properties matter more than the rule list:
 *
 *  - The file set is *derived* by walking `apps/-/src` and
 *    `packages/-/src`, so a new app or package is covered the day it is
 *    created rather than the day somebody remembers to add it here.
 *  - It is checked against an AST, not against the file's text. A regex
 *    over source matches the same characters inside a comment or a
 *    string, which is how a check comes to "cover" a pattern it has
 *    never actually seen.
 *
 * Exempting a line needs the marker below plus a reason; the marker
 * alone does not count.
 */

const EXEMPT_MARKER = 'zone-exempt:';

interface Violation {
  file: string;
  line: number;
  rule: string;
  detail: string;
}

interface ScanResult {
  violations: Violation[];
  filesScanned: number;
  nodesWalked: number;
  exemptionsHonoured: number;
}

function repoRoot(): string {
  let dir = process.cwd();
  for (;;) {
    if (existsSync(path.join(dir, 'testdata', 'recurrence_golden.json'))) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) throw new Error('could not locate the repository root');
    dir = parent;
  }
}

/**
 * Every `src` directory under `apps/` and `packages/`, discovered by
 * reading those directories rather than from a list kept here.
 */
function scanRoots(root: string): string[] {
  const roots: string[] = [];
  for (const group of ['apps', 'packages']) {
    const groupDir = path.join(root, group);
    if (!existsSync(groupDir)) continue;
    for (const entry of readdirSync(groupDir)) {
      const src = path.join(groupDir, entry, 'src');
      if (existsSync(src) && statSync(src).isDirectory()) roots.push(src);
    }
  }
  return roots.sort();
}

function collectSourceFiles(dir: string, out: string[]): void {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name === 'generated') {
        continue;
      }
      collectSourceFiles(full, out);
    } else if (/\.tsx?$/.test(entry.name) && !entry.name.endsWith('.d.ts')) {
      out.push(full);
    }
  }
}

/** A line carries an exemption only if the marker is followed by a reason. */
function exemptLines(source: string): Set<number> {
  const lines = source.split('\n');
  const exempt = new Set<number>();
  lines.forEach((text, index) => {
    const at = text.indexOf(EXEMPT_MARKER);
    if (at === -1) return;
    const reason = text.slice(at + EXEMPT_MARKER.length).trim();
    if (reason.length < 8) return;
    // The marker covers its own line and the line after it, so it can sit
    // above the expression as well as at the end of it.
    exempt.add(index + 1);
    exempt.add(index + 2);
  });
  return exempt;
}

function isIdent(node: Node | null | undefined, name: string): boolean {
  return node?.type === 'Identifier' && node.name === name;
}

/** `Intl.DateTimeFormat` as a callee, however it is spelled. */
function isIntlDateTimeFormat(node: Node): boolean {
  return (
    node.type === 'MemberExpression' &&
    isIdent(node.object, 'Intl') &&
    isIdent(node.property, 'DateTimeFormat')
  );
}

/** The static member name of a call's callee, e.g. `fromISO`. */
function calleeMember(call: CallExpression): { object: string; member: string } | null {
  const callee = call.callee;
  if (callee.type !== 'MemberExpression' || callee.computed) return null;
  if (callee.property.type !== 'Identifier') return null;
  if (callee.object.type !== 'Identifier') return null;
  return { object: callee.object.name, member: callee.property.name };
}

/** Whether an options-object argument mentions a zone at all. */
function argumentCarriesZone(node: Node | undefined): boolean {
  if (!node) return false;
  if (node.type !== 'ObjectExpression') {
    // A spread or a variable: assume the author routed a zone through it
    // rather than guess. The rule below only fires on a *missing*
    // argument or a literal object that plainly has no zone in it.
    return true;
  }
  return node.properties.some((prop) => {
    if (prop.type !== 'ObjectProperty' && prop.type !== 'ObjectMethod') return true;
    const key = prop.key;
    if (key.type === 'Identifier') return key.name === 'zone' || key.name === 'setZone';
    if (key.type === 'StringLiteral') return key.value === 'zone' || key.value === 'setZone';
    return true;
  });
}

/** An ISO literal that states its own offset is an unambiguous instant. */
function literalStatesOffset(node: Node | undefined): boolean {
  if (!node) return false;
  if (node.type === 'StringLiteral') return /(?:Z|[+-]\d{2}:?\d{2})$/i.test(node.value.trim());
  if (node.type === 'TemplateLiteral' && node.expressions.length === 0) {
    const raw = node.quasis.map((q) => q.value.cooked ?? '').join('');
    return /(?:Z|[+-]\d{2}:?\d{2})$/i.test(raw.trim());
  }
  return false;
}

// Luxon constructors that adopt the host zone unless told otherwise.
const HOST_ZONE_CONSTRUCTORS = new Set([
  'fromJSDate',
  'fromSeconds',
  'fromMillis',
  'fromObject',
  'fromFormat',
]);

function scan(files: string[]): ScanResult {
  const violations: Violation[] = [];
  let nodesWalked = 0;
  let exemptionsHonoured = 0;

  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const exempt = exemptLines(source);
    const ast = parse(source, {
      sourceType: 'module',
      plugins: ['typescript', 'jsx', 'decorators-legacy'],
      errorRecovery: true,
    });

    // `setZone` chained onto a constructor rescues it; record the calls
    // that are the object of a `.setZone(...)` member chain.
    const rescued = new Set<CallExpression>();

    const report = (node: Node, rule: string, detail: string): void => {
      const line = node.loc?.start.line ?? 0;
      if (exempt.has(line)) {
        exemptionsHonoured++;
        return;
      }
      violations.push({ file, line, rule, detail });
    };

    const walk = (node: Node | null | undefined): void => {
      if (!node || typeof node.type !== 'string') return;
      nodesWalked++;

      if (node.type === 'CallExpression') {
        const callee = node.callee;
        if (
          callee.type === 'MemberExpression' &&
          !callee.computed &&
          isIdent(callee.property, 'setZone') &&
          callee.object.type === 'CallExpression'
        ) {
          rescued.add(callee.object);
        }
      }

      for (const key of Object.keys(node) as Array<keyof Node>) {
        if (key === 'loc' || key === 'leadingComments' || key === 'trailingComments') continue;
        const value = (node as unknown as Record<string, unknown>)[key];
        if (Array.isArray(value)) {
          for (const child of value) walk(child as Node);
        } else if (value && typeof value === 'object' && 'type' in value) {
          walk(value as Node);
        }
      }
    };

    walk(ast.program);

    // Second pass, now that `rescued` is populated.
    const check = (node: Node | null | undefined): void => {
      if (!node || typeof node.type !== 'string') return;

      if (node.type === 'MemberExpression' && isIdent(node.property, 'timeZone')) {
        const inner = node.object;
        if (
          inner.type === 'CallExpression' &&
          inner.callee.type === 'MemberExpression' &&
          isIdent(inner.callee.property, 'resolvedOptions') &&
          inner.callee.object.type === 'CallExpression' &&
          isIntlDateTimeFormat(inner.callee.object.callee)
        ) {
          report(
            node,
            'host-zone-read',
            'Intl.DateTimeFormat().resolvedOptions().timeZone reads the host zone; use Zone.browser()',
          );
        }
      }

      if (node.type === 'CallExpression') {
        const member = calleeMember(node);
        if (member?.object === 'DateTime') {
          if (member.member === 'local' || member.member === 'now') {
            report(
              node,
              'host-zone-datetime',
              `DateTime.${member.member}() is anchored to the host zone; pass a resolved Zone`,
            );
          } else if (member.member === 'fromISO') {
            if (
              !argumentCarriesZone(node.arguments[1] as Node | undefined) &&
              !literalStatesOffset(node.arguments[0] as Node | undefined) &&
              !rescued.has(node)
            ) {
              report(
                node,
                'unzoned-parse',
                'DateTime.fromISO without a zone falls back to the host zone; pass { zone: zone.name }',
              );
            }
          } else if (HOST_ZONE_CONSTRUCTORS.has(member.member)) {
            if (!argumentCarriesZone(node.arguments[1] as Node | undefined) && !rescued.has(node)) {
              report(
                node,
                'unzoned-parse',
                `DateTime.${member.member} without a zone adopts the host zone; pass { zone: zone.name }`,
              );
            }
          }
        }
      }

      for (const key of Object.keys(node) as Array<keyof Node>) {
        if (key === 'loc' || key === 'leadingComments' || key === 'trailingComments') continue;
        const value = (node as unknown as Record<string, unknown>)[key];
        if (Array.isArray(value)) {
          for (const child of value) check(child as Node);
        } else if (value && typeof value === 'object' && 'type' in value) {
          check(value as Node);
        }
      }
    };

    check(ast.program);
  }

  return { violations, filesScanned: files.length, nodesWalked, exemptionsHonoured };
}

const root = repoRoot();
const roots = scanRoots(root);
const files: string[] = [];
for (const dir of roots) collectSourceFiles(dir, files);
const result = scan(files);

describe('the zone gate itself', () => {
  // A scan that silently found nothing to look at passes every rule
  // below. These assertions are what stop that: they fail if the tree
  // walk stops finding packages, if the parser stops producing nodes,
  // or if the file list collapses.
  it('derives a non-empty set of roots from the workspace layout', () => {
    expect(roots.length).toBeGreaterThanOrEqual(4);
    expect(roots.some((r) => r.includes(`${path.sep}flow-web${path.sep}`))).toBe(true);
    expect(roots.some((r) => r.includes(`${path.sep}ui${path.sep}`))).toBe(true);
  });

  it('scans a non-empty set of files and actually parses them', () => {
    expect(result.filesScanned).toBeGreaterThan(200);
    expect(result.nodesWalked).toBeGreaterThan(100_000);
  });

  it('flags the patterns it claims to flag', () => {
    // A positive control. If the detector is broken or the rules stop
    // matching, this fails even when the tree happens to be clean, so a
    // green run below always means "looked and found nothing" rather
    // than "did not look".
    const probe = scanSource(
      [
        'const a = Intl.DateTimeFormat().resolvedOptions().timeZone;',
        'const b = DateTime.local();',
        'const c = DateTime.now();',
        'const d = DateTime.fromISO(someString);',
        'const e = DateTime.fromJSDate(someDate);',
        'const f = DateTime.fromSeconds(n);',
      ].join('\n'),
    );
    expect(probe.map((v) => v.rule)).toEqual([
      'host-zone-read',
      'host-zone-datetime',
      'host-zone-datetime',
      'unzoned-parse',
      'unzoned-parse',
      'unzoned-parse',
    ]);
  });

  it('does not flag calls that name their zone, and honours a reasoned exemption', () => {
    // A negative control: each of these is a correct call, and a rule
    // that flagged them would push authors to blanket-exempt whole
    // files.
    const clean = scanSource(
      [
        'const a = DateTime.fromISO(s, { zone: zone.name });',
        "const b = DateTime.fromISO('2026-03-01T09:00:00Z');",
        'const c = DateTime.fromJSDate(d).setZone(zone.name);',
        'const e = DateTime.fromSeconds(n, { zone: zone.name });',
        'const f = Intl.DateTimeFormat().resolvedOptions().timeZone; // zone-exempt: sanctioned host read',
      ].join('\n'),
    );
    expect(clean).toEqual([]);
  });

  it('ignores a bare marker with no reason', () => {
    const notExempt = scanSource('const a = DateTime.now(); // zone-exempt:');
    expect(notExempt).toHaveLength(1);
  });

  it('does not mistake a mention in a comment or a string for real code', () => {
    // The failure this guards is a check that "covers" a pattern it has
    // only ever seen quoted.
    const inert = scanSource(
      [
        '// Intl.DateTimeFormat().resolvedOptions().timeZone is what we must not do',
        "const doc = 'DateTime.now() and DateTime.local() are host-zone bound';",
      ].join('\n'),
    );
    expect(inert).toEqual([]);
  });
});

describe('zone resolution is centralised', () => {
  it('has no unexempted host-zone read or unzoned parse in the TypeScript tree', () => {
    const rendered = result.violations.map(
      (v) => `${path.relative(root, v.file)}:${v.line} [${v.rule}] ${v.detail}`,
    );
    expect(rendered).toEqual([]);
  });
});

/**
 * Run a source string through [scan] itself — the same parser, the same
 * rules, the same exemption handling — so the controls above cannot
 * drift from the check that guards the tree.
 */
function scanSource(source: string): Violation[] {
  const dir = mkdtempSync(path.join(tmpdir(), 'zone-gate-'));
  const file = path.join(dir, 'probe.tsx');
  try {
    writeFileSync(file, source, 'utf8');
    return scan([file]).violations;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
