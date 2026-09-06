/**
 * The month-scroll date square is the primary target on the mobile
 * calendar — every day is opened through it — and its size is pure CSS.
 * Nothing in the component tree records how large the box ends up and
 * jsdom lays nothing out, so a rule that stops meeting the touch bar
 * produces no failing render test.
 *
 * Written as a sibling of the Button primitive's own touch-target test
 * rather than folded into it: this rule lives in an app stylesheet, and
 * a test inside `packages/ui` reaching down into a consumer would make
 * the package untestable on its own. The technique is the same — resolve
 * the declaration chain rather than match the rule text, so retuning
 * `--nf-space-8` or reaching for a smaller step fails here.
 *
 * The bar offset is checked against the square rather than against a
 * number of its own. The two move together — that offset is the head's
 * height plus the gap under it — and growing one without the other puts
 * the multi-day bars inside the date square.
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { describe, expect, it } from 'vitest';

/** Minimum hit area, in CSS pixels, for anything interactive. */
const MIN_TOUCH_TARGET_PX = 32;

/** Root font size CSS resolves `rem` against by default. */
const ROOT_FONT_PX = 16;

const testDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(testDir, '../../../../../..');
const monthScrollCss = readFileSync(resolve(testDir, '../month-scroll.module.css'), 'utf-8');

/** The spacing scale, read through the export the app's entry CSS uses. */
function readBaseTokens(): string {
  const pkg = JSON.parse(readFileSync(resolve(repoRoot, 'packages/ui/package.json'), 'utf-8')) as {
    exports: Record<string, string>;
  };
  const target = pkg.exports['./tokens/base.css'];
  expect(target, 'packages/ui no longer exports ./tokens/base.css').toBeDefined();
  return readFileSync(resolve(repoRoot, 'packages/ui', target ?? ''), 'utf-8');
}

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
function ruleBody(css: string, selector: string): string {
  // Anchored at a line start so a selector is never picked up out of the
  // middle of a comment, and so `.dayHead` does not match `.dayHead--today`.
  const body = css.match(new RegExp(`(?:^|\\n)\\s*${selector}\\s*{([^}]*)}`))?.[1];
  expect(body, `no rule for ${selector.replace(/\\/g, '')}`).toBeDefined();
  return body ?? '';
}

/** A single declaration's value. */
function declaration(body: string, property: string): string {
  const value = body.match(new RegExp(`(?:^|[;{\\s])${property}:\\s*([^;]+);`))?.[1];
  expect(value, `no ${property} declared`).toBeDefined();
  return (value ?? '').trim();
}

/** Resolve a spacing-token reference against the scale and convert rem to px. */
function resolvePx(value: string, scale: Map<string, string>): number {
  const token = value.match(/var\((--nf-space-[\w-]+)\)/)?.[1];
  expect(token, `"${value}" is not a spacing token reference`).toBeDefined();
  const declared = scale.get(token ?? '');
  expect(declared, `${token} is not declared in the spacing scale`).toBeDefined();
  return remToPx(declared ?? '', String(token));
}

/** A bare `Nrem` literal in pixels. */
function remToPx(value: string, label: string): number {
  const rem = value.match(/^([\d.]+)rem$/)?.[1];
  expect(rem, `${label} is "${value}", which is not a rem value`).toBeDefined();
  return Number(rem) * ROOT_FONT_PX;
}

describe('month-scroll day cell touch target', () => {
  const scale = spacingScale(readBaseTokens());
  // The same width the route's `useIsMobile` switches at, as CSS sees it.
  const condition = `(max-width: ${BP.md - 1}px)`;
  const mobile = mediaBlock(monthScrollCss, condition);

  it('has a mobile block at the breakpoint the route switches on', () => {
    expect(mobile, `month-scroll.module.css has no "@media ${condition}" block`).not.toBeNull();
  });

  it('raises the date square to the touch bar on both axes', () => {
    const head = ruleBody(mobile ?? '', '\\.dayHead');
    for (const property of ['min-inline-size', 'block-size']) {
      expect(resolvePx(declaration(head, property), scale)).toBeGreaterThanOrEqual(
        MIN_TOUCH_TARGET_PX,
      );
    }
  });

  it('moves the bar overlay by the same amount the square grew', () => {
    // Outside the media query: the wide-viewport pair, which sets the gap.
    const baseHeadPx = remToPx(
      declaration(ruleBody(monthScrollCss, '\\.dayHead'), 'block-size'),
      '.dayHead block-size',
    );
    const baseOverlayPx = remToPx(
      declaration(ruleBody(monthScrollCss, '\\.barOverlay'), 'inset-block-start'),
      '.barOverlay inset-block-start',
    );
    const gapPx = baseOverlayPx - baseHeadPx;
    expect(gapPx, 'the bars would start inside the date square').toBeGreaterThan(0);

    const mobileHeadPx = resolvePx(
      declaration(ruleBody(mobile ?? '', '\\.dayHead'), 'block-size'),
      scale,
    );
    const mobileOverlayPx = remToPx(
      declaration(ruleBody(mobile ?? '', '\\.barOverlay'), 'inset-block-start'),
      '.barOverlay inset-block-start below md',
    );
    expect(mobileOverlayPx).toBe(mobileHeadPx + gapPx);
  });
});
