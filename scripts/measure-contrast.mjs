#!/usr/bin/env node
// measure-contrast — report WCAG contrast for token pairs, per theme.
//
// Reads the oklch() values straight out of the theme files rather than
// transcribing them, so a value that changes in a theme changes here too.
// Alpha is ignored: a translucent surface composites over the page
// background, and the page background is what the ratio is taken against.
//
// unreachable-by-design: an instrument, not an assertion. It takes the
// token pair to measure as arguments and prints a ratio; which pairings
// matter is a design question with no fixed answer, so there is no set
// of arguments a gate could run it with. Contrast that is decided is
// asserted in the theme tests instead.
//
// Usage:
//   node scripts/measure-contrast.mjs <fg> <bg> [<fg> <bg> ...]
//
// Names are given without the `--` prefix, e.g.
//   node scripts/measure-contrast.mjs nf-color-accent nf-color-bg

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

const THEMES = [
  'packages/ui/src/tokens/semantic.css',
  'packages/ui/src/themes/aurora-light.css',
  'packages/ui/src/themes/aurora-dark.css',
  'packages/ui/src/themes/dotline-light.css',
  'packages/ui/src/themes/dotline-dark.css',
  'packages/ui/src/themes/glass-light.css',
  'packages/ui/src/themes/glass-dark.css',
];

/** oklch(L% C H[ / A]) -> [L, C, H], alpha dropped. */
function parseOklch(value) {
  const m = value.match(/oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*[\d.]+)?\s*\)/);
  if (!m) return null;
  return [Number(m[1]) / 100, Number(m[2]), Number(m[3])];
}

function readTheme(path) {
  const src = readFileSync(join(repo, path), 'utf8');
  const out = new Map();
  for (const m of src.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    const c = parseOklch(m[2] ?? '');
    if (c) out.set(m[1], c);
  }
  return out;
}

function toLinearSrgb(L, C, Hdeg) {
  const h = (Hdeg * Math.PI) / 180;
  const a = C * Math.cos(h);
  const b = C * Math.sin(h);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
}

function luminance(c) {
  const [r, g, b] = toLinearSrgb(...c).map((v) => Math.min(1, Math.max(0, v)));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a, b) {
  const x = luminance(a);
  const y = luminance(b);
  const [hi, lo] = x > y ? [x, y] : [y, x];
  return (hi + 0.05) / (lo + 0.05);
}

const args = process.argv.slice(2);
if (args.length === 0 || args.length % 2 !== 0) {
  console.error('usage: measure-contrast.mjs <fg> <bg> [<fg> <bg> ...]');
  process.exit(2);
}

const themes = THEMES.map((p) => [p.split('/').pop()?.replace('.css', '') ?? p, readTheme(p)]);

for (let i = 0; i < args.length; i += 2) {
  const fgName = `--${args[i]}`;
  const bgName = `--${args[i + 1]}`;
  console.log(`\n${fgName} on ${bgName}`);
  for (const [name, tokens] of themes) {
    const fg = tokens.get(fgName);
    const bg = tokens.get(bgName);
    if (!fg || !bg) {
      console.log(`  ${name.padEnd(16)} (not defined here)`);
      continue;
    }
    const r = contrast(fg, bg);
    console.log(
      `  ${name.padEnd(16)} ${r.toFixed(2).padStart(6)}  ${r >= 4.5 ? 'AA' : r >= 3 ? 'large only' : 'below 3'}`,
    );
  }
}
