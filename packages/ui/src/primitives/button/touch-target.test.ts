/**
 * A button's hit area is pure CSS: nothing in the component tree records
 * how large the box ends up, and jsdom lays nothing out, so a rule that
 * silently stops meeting the touch bar produces no failing render test.
 *
 * This resolves the chain instead of matching the rule text — the media
 * query is checked against the breakpoint constant the app's own mobile
 * switch reads, and the declared token is looked up in the spacing scale
 * and converted to pixels. Dropping the rule, retuning `--nf-space-8`,
 * or reaching for a step below it all fail here.
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { BP } from '../../tokens/breakpoints';

/** Minimum hit area, in CSS pixels, for anything interactive. */
const MIN_TOUCH_TARGET_PX = 32;

/** Root font size CSS resolves `rem` against by default. */
const ROOT_FONT_PX = 16;

const testDir = dirname(fileURLToPath(import.meta.url));
const buttonCss = readFileSync(resolve(testDir, 'button.module.css'), 'utf-8');
const baseCss = readFileSync(resolve(testDir, '../../tokens/base.css'), 'utf-8');

/** `--nf-space-*` custom property name → declared value. */
function spacingScale(css: string): Map<string, string> {
  const scale = new Map<string, string>();
  for (const [, name, value] of css.matchAll(/(--nf-space-[\w-]+):\s*([^;]+);/g)) {
    if (name && value) scale.set(name, value.trim());
  }
  return scale;
}

/** Body of the first `@media` block whose condition matches `condition`. */
function mediaBlock(css: string, condition: string): string | null {
  const start = css.indexOf(`@media ${condition}`);
  if (start === -1) return null;
  const open = css.indexOf('{', start);
  if (open === -1) return null;
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}') {
      depth--;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  return null;
}

/** Declarations of the first rule in `css` whose selector list is `selector`. */
function ruleBody(css: string, selector: string): string | null {
  // Anchored at a line start so a selector is never picked up out of the
  // middle of a comment, and so `.root` does not match `.root:hover`.
  return css.match(new RegExp(`(?:^|\\n)\\s*${selector}\\s*{([^}]*)}`))?.[1] ?? null;
}

/** Resolve a spacing-token reference against the scale and convert rem to px. */
function resolvePx(value: string, scale: Map<string, string>): number {
  const token = value.match(/var\((--nf-space-[\w-]+)\)/)?.[1];
  expect(token, `"${value}" is not a spacing token reference`).toBeDefined();
  const declared = scale.get(token ?? '');
  expect(declared, `${token} is not declared in the spacing scale`).toBeDefined();
  const rem = declared?.match(/^([\d.]+)rem$/)?.[1];
  expect(rem, `${token} is "${declared ?? ''}", which is not a rem value`).toBeDefined();
  return Number(rem) * ROOT_FONT_PX;
}

describe('Button touch target', () => {
  const scale = spacingScale(baseCss);
  // The same width `useIsMobile` switches at, expressed as CSS sees it.
  const condition = `(max-width: ${BP.md - 1}px)`;

  it('raises the floor below the md breakpoint', () => {
    const block = mediaBlock(buttonCss, condition);
    expect(block, `button.module.css has no "@media ${condition}" block`).not.toBeNull();

    const declarations = ruleBody(block ?? '', '\\.root');
    expect(declarations, 'the mobile block does not target .root').not.toBeNull();

    for (const property of ['min-inline-size', 'min-block-size']) {
      const value = declarations?.match(new RegExp(`${property}:\\s*([^;]+);`))?.[1];
      expect(value, `.root declares no ${property} below md`).toBeDefined();
      expect(resolvePx(value ?? '', scale)).toBeGreaterThanOrEqual(MIN_TOUCH_TARGET_PX);
    }
  });

  it('leaves the wide-viewport geometry alone', () => {
    // The floor belongs to the fingertip, not to the component: a size
    // written outside the media query would move every toolbar and table
    // row in the product.
    const outsideMedia = buttonCss.slice(0, buttonCss.indexOf(`@media ${condition}`));
    expect(outsideMedia).not.toMatch(/min-(inline|block)-size:/);
  });
});
