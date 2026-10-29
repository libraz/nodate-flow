import { readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/**
 * Reduced motion is one switch: the media query in base.css zeroes the
 * duration tokens, and everything that moves takes its duration from a
 * token. A literal `200ms`, or a duration token the media query forgot,
 * keeps moving for a reader who asked it not to, and no render test can
 * see it. These checks read the stylesheets instead.
 */

const SRC = resolve(import.meta.dirname, '..');
const base = readFileSync(join(SRC, 'tokens/base.css'), 'utf8');
const semantic = readFileSync(join(SRC, 'tokens/semantic.css'), 'utf8');
const MEDIA = '@media (prefers-reduced-motion: reduce)';

function stripComments(css: string): string {
  let out = '';
  let i = 0;
  while (i < css.length) {
    const open = css.indexOf('/*', i);
    if (open === -1) {
      out += css.slice(i);
      break;
    }
    out += css.slice(i, open);
    const close = css.indexOf('*/', open + 2);
    i = close === -1 ? css.length : close + 2;
  }
  return out;
}

/** Body of the block opened by the first occurrence of `head`. */
function blockAfter(css: string, head: string): string {
  const start = css.indexOf(head);
  if (start === -1) return '';
  const open = css.indexOf('{', start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}') {
      depth--;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  return '';
}

/** Custom property names declared (`--name:`) in `css` that start with `prefix`. */
function declaredProps(css: string, prefix: string): string[] {
  const names: string[] = [];
  let i = css.indexOf(prefix);
  while (i !== -1) {
    const colon = css.indexOf(':', i);
    const name = css.slice(i, colon).trim();
    if (colon !== -1 && !name.includes(')') && !name.includes(' ')) names.push(name);
    i = css.indexOf(prefix, i + prefix.length);
  }
  return names;
}

/** Values of every `<property>:` declaration whose name is in `properties`. */
function declarationValues(css: string, properties: string[]): string[] {
  const values: string[] = [];
  for (const property of properties) {
    let i = css.indexOf(`${property}:`);
    while (i !== -1) {
      const before = css[i - 1];
      // A whole property name, not the tail of another (`-webkit-…`, `--x-…`).
      if (before === undefined || before === ' ' || before === '\n' || before === '{') {
        const end = css.indexOf(';', i);
        values.push(css.slice(i + property.length + 1, end === -1 ? undefined : end));
      }
      i = css.indexOf(`${property}:`, i + property.length);
    }
  }
  return values;
}

/** Words of a CSS value that are a literal time (`120ms`, `.2s`). */
function literalTimes(value: string): string[] {
  const words: string[] = [];
  let word = '';
  for (const ch of `${value} `) {
    if (' \n\t,()*/+'.includes(ch)) {
      if (word) words.push(word);
      word = '';
    } else {
      word += ch;
    }
  }
  return words.filter((w) => {
    const digits = w.endsWith('ms') ? w.slice(0, -2) : w.endsWith('s') ? w.slice(0, -1) : '';
    return digits !== '' && Number.isFinite(Number(digits));
  });
}

function componentStylesheets(): string[] {
  return readdirSync(SRC, { recursive: true, encoding: 'utf8' })
    .filter((path) => path.endsWith('.css'))
    .filter((path) => !path.startsWith('tokens/') && !path.startsWith('themes/'))
    .sort();
}

describe('reduced motion token cascade', () => {
  it('uses higher specificity than theme selectors loaded later by apps', () => {
    expect(base).toContain(MEDIA);
    expect(base).toContain(':root:root');
    expect(base).toContain('--nf-motion-duration: 0ms');
    expect(semantic).toContain(MEDIA);
    expect(semantic).toContain(':root:root');
    expect(semantic).toContain('--nf-surface-blur: 0px');
  });

  it('zeroes every duration token base.css declares', () => {
    const plain = stripComments(base);
    const declared = new Set(declaredProps(blockAfter(plain, ':where(:root)'), '--nf-duration-'));
    expect(declared.size).toBeGreaterThan(0);

    const reduced = blockAfter(blockAfter(plain, MEDIA), ':root:root');
    for (const name of declared) {
      expect(reduced, `${name} is not zeroed under reduced motion`).toContain(`${name}: 0ms`);
    }
  });
});

describe('component motion', () => {
  const sheets = componentStylesheets();

  it('finds the stylesheets and declarations it checks', () => {
    expect(sheets.length).toBeGreaterThan(20);
    // A parser that stopped seeing declarations would pass every check below.
    const all = sheets.flatMap((path) =>
      declarationValues(stripComments(readFileSync(join(SRC, path), 'utf8')), [
        'animation',
        'transition',
      ]),
    );
    expect(all.length).toBeGreaterThan(40);
    expect(literalTimes('opacity 120ms ease, color .2s')).toEqual(['120ms', '.2s']);
  });

  it.each(sheets)('%s takes every animation and transition duration from a token', (path) => {
    const css = stripComments(readFileSync(join(SRC, path), 'utf8'));
    const values = declarationValues(css, [
      'animation',
      'animation-duration',
      'animation-delay',
      'transition',
      'transition-duration',
      'transition-delay',
    ]);
    for (const value of values) {
      expect(literalTimes(value), `literal time in "${value.trim()}"`).toEqual([]);
    }
  });

  it.each(sheets)('%s does not branch on prefers-reduced-motion', (path) => {
    const css = stripComments(readFileSync(join(SRC, path), 'utf8'));
    expect(css).not.toContain('prefers-reduced-motion');
  });

  it('every global keyframe a stylesheet names is defined in styles/motion.css', () => {
    const motion = stripComments(readFileSync(join(SRC, 'styles/motion.css'), 'utf8'));
    for (const path of sheets) {
      const css = stripComments(readFileSync(join(SRC, path), 'utf8'));
      let i = css.indexOf('global(');
      while (i !== -1) {
        const name = css.slice(i + 'global('.length, css.indexOf(')', i)).trim();
        expect(motion, `${path} names undefined keyframes ${name}`).toContain(
          `@keyframes ${name} {`,
        );
        i = css.indexOf('global(', i + 1);
      }
    }
  });
});
