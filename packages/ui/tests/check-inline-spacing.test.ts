/**
 * Unit tests for the inline spacing scanner.
 *
 * The scanner walks JSX / TS / CSS source looking for hardcoded
 * `<number>rem` / `<number>px` (>= 4) values applied to spacing,
 * sizing, radius, or font-size properties. Tests below feed in
 * synthetic snippets so we do not depend on the actual repo state.
 */

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { formatterHint, scanFiles, scanSource } from '../scripts/check-inline-spacing';

describe('scanSource', () => {
  it('flags inline rem on a tokened property', () => {
    const offenses = scanSource(
      '/virtual/markdown-editor.tsx',
      "const x = <div style={{ gap: '0.375rem' }} />;\n",
    );
    expect(offenses).toHaveLength(1);
    const first = offenses[0];
    if (!first) throw new Error('expected at least one offense');
    expect(first.property).toBe('gap');
    expect(first.value).toBe('0.375rem');
    expect(first.line).toBe(1);
  });

  it('flags multiple offenses on the same line', () => {
    const offenses = scanSource(
      '/virtual/box.tsx',
      "<div style={{ paddingTop: '1.5rem', marginInlineStart: '0.5rem' }} />\n",
    );
    expect(offenses.map((o) => o.property).sort()).toEqual(
      ['margin-inline-start', 'padding-top'].sort(),
    );
  });

  it('does not flag values that already use a token', () => {
    const offenses = scanSource(
      '/virtual/ok.tsx',
      "<div style={{ gap: 'var(--nf-space-2)', padding: 'var(--nf-space-4)' }} />\n",
    );
    expect(offenses).toEqual([]);
  });

  it('honours the nf-token-override marker for whole files', () => {
    const offenses = scanSource(
      '/virtual/source-colors.ts',
      [
        '/* nf-token-override: persisted by API */',
        "export const X = { padding: '0.375rem', gap: '12px' };",
      ].join('\n'),
    );
    expect(offenses).toEqual([]);
  });

  it('flags px values >= 4 but tolerates 1px hairlines', () => {
    const offenses = scanSource(
      '/virtual/border.tsx',
      [
        "const a = <div style={{ borderRadius: '12px' }} />;",
        "const b = <div style={{ marginTop: '2px' }} />;",
      ].join('\n'),
    );
    expect(offenses).toHaveLength(1);
    const only = offenses[0];
    if (!only) throw new Error('expected one offense');
    expect(only.property).toBe('border-radius');
    expect(only.value).toBe('12px');
  });

  it('flags raw CSS rule bodies, not just JSX inline styles', () => {
    const offenses = scanSource(
      '/virtual/local.css',
      `.thing {
        gap: 0.75rem;
        padding-inline-start: 1rem;
        border-radius: 8px;
      }
      `,
    );
    const props = offenses.map((o) => o.property).sort();
    expect(props).toEqual(['border-radius', 'gap', 'padding-inline-start']);
  });

  it('skips zero / keyword values', () => {
    const offenses = scanSource(
      '/virtual/zero.tsx',
      ["<div style={{ margin: 0, padding: 'auto', width: '100%' }} />"].join('\n'),
    );
    expect(offenses).toEqual([]);
  });

  it('does not flag non-tokened properties (e.g. line-height)', () => {
    const offenses = scanSource(
      '/virtual/ignored.tsx',
      "<div style={{ lineHeight: '1.5rem' }} />\n",
    );
    expect(offenses).toEqual([]);
  });

  it('skips JS / CSS comments', () => {
    const offenses = scanSource(
      '/virtual/commented.tsx',
      [
        "// const dead = <div style={{ gap: '0.75rem' }} />",
        '/* gap: 1rem; */',
        "<div style={{ gap: 'var(--nf-space-2)' }} />",
      ].join('\n'),
    );
    expect(offenses).toEqual([]);
  });

  it('flags font-size and minInlineSize together', () => {
    const offenses = scanSource(
      '/virtual/typography.tsx',
      "<button style={{ fontSize: '0.8125rem', minInlineSize: '2rem' }} />\n",
    );
    const props = offenses.map((o) => o.property).sort();
    expect(props).toEqual(['font-size', 'min-inline-size']);
  });
});

/**
 * The annotation rule is normatively `scripts/lib/token-override.mjs`,
 * which the colour scan imports. This scanner cannot import it: it
 * compiles under `packages/ui` with `rootDir: "."`, so a path outside the
 * package would break the build. It therefore keeps a copy, and a copy is
 * only safe while something notices it drifting — the two checks reading
 * the same marker by two different rules is what put 146 files outside
 * the colour scan without anyone writing a colour exemption.
 */
describe('annotation rule', () => {
  it('matches the shared implementation byte for byte', () => {
    const shared = readFileSync(
      resolve(import.meta.dirname, '../../../scripts/lib/token-override.mjs'),
      'utf8',
    );
    const own = readFileSync(
      resolve(import.meta.dirname, '../scripts/check-inline-spacing.ts'),
      'utf8',
    );
    const reason = (src: string): string | undefined =>
      src.match(/REASON = String\.raw`([^`]+)`/)?.[1];
    expect(reason(own)).toBeDefined();
    expect(reason(own)).toBe(reason(shared));
  });
});

/**
 * An annotation is scoped to two lines, so where it sits is part of what
 * it means — and the formatter is free to move comments. It relocates a
 * comment written after an at-rule's `{` onto the next line, which puts
 * the at-rule outside the window and retires the exemption without
 * changing a character of the annotation itself.
 */
describe('formatterHint', () => {
  it('names the formatter when an annotation sits inside the block it was written for', () => {
    const src = [
      '@media (min-width: 100rem) {',
      '  /* nf-token-override: at-rule condition, not a spacing step */',
      '  .layout { color: red; }',
      '}',
    ].join('\n');
    expect(formatterHint(src, 1)).toContain('line 2');
    expect(formatterHint(src, 1)).toContain('directly above the at-rule');
  });

  it('stays silent for an annotation correctly placed above the at-rule', () => {
    const src = [
      '/* nf-token-override: at-rule condition, not a spacing step */',
      '@media (min-width: 100rem) {',
      '  .layout { color: red; }',
      '}',
    ].join('\n');
    expect(formatterHint(src, 2)).toBeUndefined();
  });

  it('stays silent for a plain declaration followed by an annotation', () => {
    const src = ['.a {', '  padding: 5px;', '  /* nf-token-override: unrelated */', '}'].join('\n');
    expect(formatterHint(src, 2)).toBeUndefined();
  });
});

describe('scanFiles dangling annotations', () => {
  let dir: string;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), 'nf-spacing-'));
  });
  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  const scan = () =>
    scanFiles({ root: dir, scanDirs: ['.'], excludeFragments: ['/node_modules/'] });

  it('reports an annotation whose window holds no literal', () => {
    writeFileSync(
      join(dir, 'a.css'),
      '.a {\n  /* nf-token-override: stale reason */\n  color: red;\n}\n',
    );
    const { offenses, dangling } = scan();
    expect(offenses).toEqual([]);
    expect(dangling.map((d) => d.line)).toEqual([2]);
  });

  it('does not report an annotation that suppressed a literal on the next line', () => {
    writeFileSync(
      join(dir, 'b.css'),
      '.b {\n  /* nf-token-override: live reason */\n  padding: 5px;\n}\n',
    );
    const { offenses, dangling } = scan();
    expect(offenses).toEqual([]);
    expect(dangling).toEqual([]);
  });

  it('does not report an annotation trailing the literal it suppressed', () => {
    writeFileSync(
      join(dir, 'c.css'),
      '.c {\n  padding: 5px; /* nf-token-override: live reason */\n}\n',
    );
    const { offenses, dangling } = scan();
    expect(offenses).toEqual([]);
    expect(dangling).toEqual([]);
  });

  it('reports the annotation and the offense when the annotation moved off its target', () => {
    writeFileSync(
      join(dir, 'd.css'),
      '@media (min-width: 100rem) {\n  /* nf-token-override: at-rule condition */\n  .d {\n    color: red;\n  }\n}\n',
    );
    const { offenses, dangling } = scan();
    expect(offenses.map((o) => o.line)).toEqual([1]);
    expect(dangling.map((d) => d.line)).toEqual([2]);
  });
});
