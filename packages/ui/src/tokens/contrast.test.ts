import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/**
 * WCAG contrast of the token pairs the primitives rely on, in every theme.
 * A value retuned in one theme file drops below AA without any render test
 * noticing, so this reads the theme files and does the colour maths:
 * oklch -> sRGB, translucent tokens composited over what they sit on.
 */

const SRC = resolve(import.meta.dirname, '..');
const THEMES = [
  'glass-light',
  'glass-dark',
  'modern-light',
  'modern-dark',
  'washi-light',
  'washi-dark',
  'classic-light',
  'classic-dark',
  'nothing-light',
  'nothing-dark',
] as const;

type Rgba = [number, number, number, number];

/** `--nf-color-*` and `--nf-cal-*` declarations of one theme file, by name. */
function colorTokens(css: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const chunk of css.split(';')) {
    const color = chunk.indexOf('--nf-color-');
    const at = color === -1 ? chunk.indexOf('--nf-cal-') : color;
    if (at === -1) continue;
    const decl = chunk.slice(at);
    const colon = decl.indexOf(':');
    if (colon === -1) continue;
    out.set(decl.slice(0, colon).trim(), decl.slice(colon + 1).trim());
  }
  return out;
}

function parseOklch(value: string): Rgba {
  expect(value.startsWith('oklch('), `"${value}" is not an oklch() literal`).toBe(true);
  let body = value.slice(6, value.lastIndexOf(')'));
  let alpha = 1;
  const slash = body.indexOf('/');
  if (slash !== -1) {
    alpha = Number(body.slice(slash + 1));
    body = body.slice(0, slash);
  }
  const [l = '', c = '', h = ''] = body.trim().split(/\s+/);
  const L = l.endsWith('%') ? Number(l.slice(0, -1)) / 100 : Number(l);
  const C = Number(c);
  const H = (Number(h) * Math.PI) / 180;
  const a = C * Math.cos(H);
  const b = C * Math.sin(H);
  const l3 = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m3 = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s3 = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const linear = [
    4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3,
    -1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3,
    -0.0041960863 * l3 - 0.7034186147 * m3 + 1.707614701 * s3,
  ];
  const encode = (x: number): number =>
    Math.min(1, Math.max(0, x <= 0.0031308 ? 12.92 * x : 1.055 * x ** (1 / 2.4) - 0.055));
  return [encode(linear[0] ?? 0), encode(linear[1] ?? 0), encode(linear[2] ?? 0), alpha];
}

function over(fg: Rgba, bg: Rgba): Rgba {
  const a = fg[3];
  return [0, 1, 2].map((i) => (fg[i] ?? 0) * a + (bg[i] ?? 0) * (1 - a)).concat(1) as Rgba;
}

function luminance(c: Rgba): number {
  const lin = (x: number): number => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2]);
}

function ratio(a: Rgba, b: Rgba): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
}

describe.each(THEMES)('%s contrast', (theme) => {
  const tokens = colorTokens(readFileSync(join(SRC, `themes/${theme}.css`), 'utf8'));
  // A bare name is a `--nf-color-*` token; `cal-*` names keep their own prefix.
  const raw = (name: string): Rgba => {
    const prop = name.startsWith('cal-') ? `--nf-${name}` : `--nf-color-${name}`;
    const value = tokens.get(prop);
    expect(value, `${prop} is not declared`).toBeDefined();
    return parseOklch(value ?? '');
  };
  const on = (name: string, backdrop: Rgba): Rgba => over(raw(name), backdrop);

  const bg = on('bg', [1, 1, 1, 1]);
  const elevated = on('bg-elevated', bg);
  const surface = on('surface', bg);
  const backdrops: Record<string, Rgba> = {
    bg,
    'bg-elevated': elevated,
    'bg-sunken': on('bg-sunken', bg),
    surface,
    'surface-hover': on('surface-hover', bg),
    'accent-subtle': on('accent-subtle', surface),
  };

  it.each(['fg-muted', 'fg-subtle'])('%s holds 4.5:1 as body text', (token) => {
    for (const [where, backdrop] of Object.entries(backdrops)) {
      const value = ratio(on(token, backdrop), backdrop);
      expect(value, `${token} on ${where}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it('border-control holds 3:1 against what a control sits on', () => {
    // The border paints over the control's own bg-elevated fill.
    const edge = on('border-control', elevated);
    for (const [where, backdrop] of Object.entries({ bg, surface, 'bg-elevated': elevated })) {
      expect(ratio(edge, backdrop), `border-control on ${where}`).toBeGreaterThanOrEqual(3);
    }
  });

  // Badge tones and colourful SegmentedControl segments share one scheme:
  // `-fg` text, bare at rest, on the `-subtle` wash over bg-elevated when selected.
  it.each([
    ['success-subtle', 'success-fg'],
    ['warning-subtle', 'warning-fg'],
    ['danger-subtle', 'danger-fg'],
    ['info-subtle', 'info-fg'],
    ['accent-subtle', 'accent-fg'],
    ['cal-task-subtle', 'cal-task-fg'],
    ['cal-event-subtle', 'cal-event-fg'],
    ['cal-block-subtle', 'cal-block-fg'],
    ['cal-free-subtle', 'cal-free-fg'],
    ['cal-milestone-subtle', 'cal-milestone-fg'],
  ])('%s with %s holds 4.5:1, washed and bare', (subtle, fg) => {
    const wash = on(subtle, elevated);
    expect(ratio(on(fg, wash), wash), 'on its wash').toBeGreaterThanOrEqual(4.5);
    for (const [where, backdrop] of Object.entries({ bg, surface, 'bg-elevated': elevated })) {
      expect(ratio(on(fg, backdrop), backdrop), `bare on ${where}`).toBeGreaterThanOrEqual(4.5);
    }
  });
});
