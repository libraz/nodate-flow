/**
 * Every event type the server can write a notification for needs a key
 * here, and every key here needs a translation in all three locales.
 *
 * The map is the only thing standing between a reader and an English
 * title: the server stores one on every row, and until this existed the
 * dropdown printed it verbatim. That failure is invisible to the checks
 * we have — the string never passes through `t()`, so the i18n guard
 * sees no key, and it is not a literal in this repo either, so the
 * hardcoded-string guard sees nothing. This test is what sees it.
 *
 * The server list is read out of `classifyEvent` rather than restated,
 * so adding a case there without a key here fails instead of shipping
 * one more English line.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { NOTIFICATION_EVENT_KEY } from '../event-keys';

const REPO = resolve(import.meta.dirname, '../../../../../..');

/** Event types classifyEvent returns a non-empty title for. */
function serverEventTypes(): string[] {
  const src = readFileSync(resolve(REPO, 'apps/flow-api/internal/notification/fanout.go'), 'utf8');
  const body = src.slice(src.indexOf('func classifyEvent('));
  const out: string[] = [];
  // `case "x":` followed by a return whose first value is a non-empty
  // string. A case that returns "" is deliberately not notified.
  for (const m of body.matchAll(/case "([a-z._]+)":\s*\n\s*return "([^"]*)"/g)) {
    if ((m[2] ?? '') !== '') out.push(m[1] as string);
  }
  return out;
}

function locale(lang: string): Record<string, unknown> {
  return JSON.parse(
    readFileSync(resolve(REPO, `apps/flow-web/locales/${lang}/notifications.json`), 'utf8'),
  ) as Record<string, unknown>;
}

describe('NOTIFICATION_EVENT_KEY', () => {
  it('covers every event type the server notifies on', () => {
    const server = serverEventTypes();
    expect(server.length).toBeGreaterThan(0);
    const missing = server.filter((e) => !(e in NOTIFICATION_EVENT_KEY));
    expect(missing).toEqual([]);
  });

  it('maps nothing the server never sends', () => {
    const server = new Set(serverEventTypes());
    const stale = Object.keys(NOTIFICATION_EVENT_KEY).filter((e) => !server.has(e));
    expect(stale).toEqual([]);
  });

  it.each(['en', 'ja', 'zh'])('has a %s translation for every key', (lang) => {
    const bundle = locale(lang);
    const event = (bundle.event ?? {}) as Record<string, string>;
    const missing = Object.values(NOTIFICATION_EVENT_KEY)
      .map((k) => k.replace(/^event\./, ''))
      .filter((k) => typeof event[k] !== 'string' || event[k].trim() === '');
    expect(missing).toEqual([]);
  });

  it('translates each locale differently from English', () => {
    const en = (locale('en').event ?? {}) as Record<string, string>;
    for (const lang of ['ja', 'zh']) {
      const other = (locale(lang).event ?? {}) as Record<string, string>;
      const untranslated = Object.keys(en).filter((k) => other[k] === en[k]);
      expect(untranslated, `${lang} still holds the English string`).toEqual([]);
    }
  });
});
