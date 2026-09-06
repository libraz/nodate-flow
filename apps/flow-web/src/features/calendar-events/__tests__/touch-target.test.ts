/**
 * Every hit area on the mobile calendar is pure CSS. Nothing in the
 * component tree records how large a box ends up and jsdom lays nothing
 * out, so a rule that stops meeting the touch bar produces no failing
 * render test.
 *
 * Written as a sibling of the Button primitive's own touch-target test
 * rather than folded into it: these rules live in app stylesheets, and a
 * test inside `packages/ui` reaching down into a consumer would make the
 * package untestable on its own. The technique is the same — resolve the
 * declaration chain rather than match the rule text, so retuning a token
 * or reaching for a smaller step fails here.
 *
 * Three boxes are covered, and they are not the same size for the same
 * reason. The day column is the only control the month view has, so it
 * carries the strictest bar. The date square inside it is no longer a
 * control but is still what a reader aims at, so it keeps the floor it
 * was raised to. And a row of the day sheet is the target that exists
 * because the month chips could not be one.
 *
 * The bar offset is checked against the date square rather than against
 * a number of its own. The two move together — that offset is the
 * square's height plus the gap under it — and growing one without the
 * other puts the multi-day bars inside the square.
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { BP } from '@nodate-flow/ui/tokens/breakpoints';
import { describe, expect, it } from 'vitest';

/**
 * Minimum hit area, in CSS pixels, for anything interactive — the floor
 * the Button primitive's own test holds every control in the product to.
 */
const MIN_TOUCH_TARGET_PX = 32;

/**
 * The bar a target carries when it is the only way to reach what it
 * opens: WCAG 2.5.5's 44px, which is what the day sheet exists to give
 * an event that the 18px month chip could not.
 */
const MIN_PRIMARY_TARGET_PX = 44;

/** Root font size CSS resolves `rem` against by default. */
const ROOT_FONT_PX = 16;

/**
 * The viewport these surfaces render at, as CSS sees it — the same width
 * the route's `useIsMobile` switches on.
 */
const MOBILE_CONDITION = `(max-width: ${BP.md - 1}px)`;

const testDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(testDir, '../../../../../..');
const monthScrollCss = readFileSync(resolve(testDir, '../month-scroll.module.css'), 'utf-8');
const daySheetCss = readFileSync(resolve(testDir, '../day-detail-sheet.module.css'), 'utf-8');

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

/**
 * Declarations of the first rule in `css` whose selector list is
 * `selector`, or null when there is none.
 */
function findRuleBody(css: string, selector: string): string | null {
  // Anchored at a line start so a selector is never picked up out of the
  // middle of a comment, and so `.dayHead` does not match `.dayHead--today`.
  return css.match(new RegExp(`(?:^|\\n)\\s*${selector}\\s*{([^}]*)}`))?.[1] ?? null;
}

/** Same, asserting the rule exists. */
function ruleBody(css: string, selector: string): string {
  const body = findRuleBody(css, selector);
  expect(body, `no rule for ${selector.replace(/\\/g, '')}`).not.toBeNull();
  return body ?? '';
}

/** A single declaration's value, or null when the property is absent. */
function findDeclaration(body: string, property: string): string | null {
  const value = body.match(new RegExp(`(?:^|[;{\\s])${property}:\\s*([^;]+);`))?.[1];
  return value === undefined ? null : value.trim();
}

/** Same, asserting the declaration exists. */
function declaration(body: string, property: string): string {
  const value = findDeclaration(body, property);
  expect(value, `no ${property} declared`).not.toBeNull();
  return value ?? '';
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

/** A size in pixels, whether it is written as a token or a rem literal. */
function sizePx(value: string, scale: Map<string, string>, label: string): number {
  return value.includes('var(') ? resolvePx(value, scale) : remToPx(value, label);
}

/**
 * What a property actually resolves to on a phone: the base declaration,
 * overridden by one inside the mobile media block when there is one.
 *
 * Reading the first match in the file instead answers for the wide
 * viewport. These surfaces only exist below the breakpoint, so a rule
 * added to the mobile block — the one context this file is about — is
 * exactly where a target would be shrunk, and is the one place a
 * first-match read never looks.
 */
function effectiveMobilePx(
  css: string,
  selector: string,
  property: string,
  scale: Map<string, string>,
): number {
  const label = `${selector.replace(/\\/g, '')} ${property} below md`;
  const base = findDeclaration(findRuleBody(css, selector) ?? '', property);
  const mobile = mediaBlock(css, MOBILE_CONDITION);
  const override =
    mobile === null ? null : findDeclaration(findRuleBody(mobile, selector) ?? '', property);
  const value = override ?? base;
  expect(value, `${label} is declared neither at the base nor in the mobile block`).not.toBeNull();
  return sizePx(value ?? '', scale, label);
}

describe('month-scroll day column touch target', () => {
  const scale = spacingScale(readBaseTokens());
  const mobile = mediaBlock(monthScrollCss, MOBILE_CONDITION);

  it('has a mobile block at the breakpoint the route switches on', () => {
    expect(
      mobile,
      `month-scroll.module.css has no "@media ${MOBILE_CONDITION}" block`,
    ).not.toBeNull();
  });

  it('keeps the day column, which is the control, at or above the primary bar', () => {
    // Read at the mobile condition, not as first-match: this view only
    // renders below `md`, so an override added to that block is what a
    // phone gets, and it is the one a first-match read would miss.
    //
    // Only the block axis is checked: the column is one seventh of the
    // viewport wide, which no rule here can raise and which a phone
    // gives about 55px of.
    expect(
      effectiveMobilePx(monthScrollCss, '\\.dayCol', 'min-block-size', scale),
    ).toBeGreaterThanOrEqual(MIN_PRIMARY_TARGET_PX);
  });

  it('declares the day column at or above the primary bar before any override', () => {
    // The base rule answers a different question from the one above —
    // what the stylesheet says outright, rather than what the cascade
    // leaves at this width — and an override that happens to be large
    // enough must not be what holds the floor up.
    const column = ruleBody(monthScrollCss, '\\.dayCol');
    expect(
      remToPx(declaration(column, 'min-block-size'), '.dayCol min-block-size'),
    ).toBeGreaterThanOrEqual(MIN_PRIMARY_TARGET_PX);
  });

  it('keeps the date square at the floor it was raised to', () => {
    // The square stopped being the press target when the whole column
    // became one, but it is still the mark a reader aims the press at,
    // and shrinking it back would make the day hard to pick out.
    for (const property of ['min-inline-size', 'block-size']) {
      expect(
        effectiveMobilePx(monthScrollCss, '\\.dayHead', property, scale),
      ).toBeGreaterThanOrEqual(MIN_TOUCH_TARGET_PX);
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

describe('day sheet row touch target', () => {
  const scale = spacingScale(readBaseTokens());

  it('keeps a row at or above the primary bar on a phone', () => {
    // The row is the whole reason the sheet exists: the month chip it
    // replaces is about 18px tall, and dropping this rule back under the
    // bar would leave the calendar with no target a finger can aim at at
    // all.
    //
    // The sheet has no mobile block today, but it is read at the mobile
    // condition anyway — the sheet only ever opens below `md`, so that
    // is where a rule shrinking it would be written, and a first-match
    // read would go on passing after one was.
    expect(
      effectiveMobilePx(daySheetCss, '\\.row', 'min-block-size', scale),
    ).toBeGreaterThanOrEqual(MIN_PRIMARY_TARGET_PX);
  });

  it('declares the row at or above the primary bar before any override', () => {
    const row = ruleBody(daySheetCss, '\\.row');
    expect(resolvePx(declaration(row, 'min-block-size'), scale)).toBeGreaterThanOrEqual(
      MIN_PRIMARY_TARGET_PX,
    );
  });
});
