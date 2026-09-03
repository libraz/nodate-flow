/**
 * Every key in the notification event map needs a translation in all
 * three locales, and no locale may carry an `event.*` entry the map never
 * reaches for.
 *
 * The map is the only thing standing between a reader and an English
 * title: the server stores one on every row, and until this existed the
 * dropdown printed it verbatim. That failure is invisible to the checks
 * we have — the string never passes through `t()`, so the i18n guard
 * sees no key, and it is not a literal in this repo either, so the
 * hardcoded-string guard sees nothing. This test is what sees it.
 *
 * Whether the map covers the event types the server actually notifies on
 * is asserted on the server side, where that set is decided: a Go test
 * loads `event-keys.json` and compares it against the fan-out's
 * classification table. This file deliberately does not restate it, and
 * does not read Go source to find out.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { NOTIFICATION_EVENT_KEY } from '../event-keys';

const REPO = resolve(import.meta.dirname, '../../../../../..');

function locale(lang: string): Record<string, unknown> {
  return JSON.parse(
    readFileSync(resolve(REPO, `apps/flow-web/locales/${lang}/notifications.json`), 'utf8'),
  ) as Record<string, unknown>;
}

/** The `event.*` sub-tree of one locale bundle. */
function localeEvents(lang: string): Record<string, string> {
  return (locale(lang).event ?? {}) as Record<string, string>;
}

/** Key names under `event.` that the map points at. */
function mappedNames(): string[] {
  return Object.values(NOTIFICATION_EVENT_KEY).map((k) => k.replace(/^event\./, ''));
}

describe('NOTIFICATION_EVENT_KEY', () => {
  it('maps at least one event type', () => {
    // Without this the two assertions below hold over an empty map and
    // report a green that means nothing.
    expect(Object.keys(NOTIFICATION_EVENT_KEY).length).toBeGreaterThan(0);
  });

  it('points every event type at a key in the event namespace', () => {
    const stray = Object.entries(NOTIFICATION_EVENT_KEY).filter(
      ([, key]) => !key.startsWith('event.'),
    );
    expect(stray).toEqual([]);
  });

  it.each(['en', 'ja', 'zh'])('has a %s translation for every key', (lang) => {
    const event = localeEvents(lang);
    const missing = mappedNames().filter(
      (k) => typeof event[k] !== 'string' || event[k].trim() === '',
    );
    expect(missing).toEqual([]);
  });

  it.each(['en', 'ja', 'zh'])('carries no unreachable %s translation', (lang) => {
    // A leftover translation is a rename that only landed on one side:
    // the copy is still there, and the event type that used to reach it
    // now renders as the English title stored on the row.
    const mapped = new Set(mappedNames());
    const unused = Object.keys(localeEvents(lang)).filter((k) => !mapped.has(k));
    expect(unused).toEqual([]);
  });

  it('translates each locale differently from English', () => {
    const en = localeEvents('en');
    for (const lang of ['ja', 'zh']) {
      const other = localeEvents(lang);
      const untranslated = Object.keys(en).filter((k) => other[k] === en[k]);
      expect(untranslated, `${lang} still holds the English string`).toEqual([]);
    }
  });
});
