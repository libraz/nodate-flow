/**
 * Unit tests for the inline spacing scanner.
 *
 * The scanner walks JSX / TS / CSS source looking for hardcoded
 * `<number>rem` / `<number>px` (>= 4) values applied to spacing,
 * sizing, radius, or font-size properties. Tests below feed in
 * synthetic snippets so we do not depend on the actual repo state.
 */

import { describe, expect, it } from 'vitest';

import { scanSource } from '../scripts/check-inline-spacing';

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
