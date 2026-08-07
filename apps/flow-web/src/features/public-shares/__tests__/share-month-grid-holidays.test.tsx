/**
 * The public share's `showHolidaysCountry` setting used to be echoed back
 * as a text chip while the grid itself drew no holidays at all: a share
 * page could advertise "Holidays: Japan" and still show 3 May as an
 * ordinary Sunday. These tests pin the overlay to the grid, so removing
 * the wiring puts the advertisement back on its own.
 */

import { render } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import en from '../../../../locales/en/common.json';
import ShareMonthGrid from '../share-month-grid';

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      ns: ['common'],
      defaultNS: 'common',
      resources: { en: { common: en } },
      interpolation: { escapeValue: false },
    });
  return instance;
}

function renderGrid(holidaysCountry: string | null): ReactElement {
  const instance = buildI18n();
  // An event anchored in May 2026 opens the grid on that month, which
  // contains Japan's Golden Week run.
  const events = [
    {
      id: 'evt-1',
      kind: 'event',
      showAs: 'busy',
      flexibility: 'fixed',
      title: 'Kickoff',
      allDay: false,
      timezone: 'UTC',
      startAt: Math.floor(Date.UTC(2026, 4, 12, 9) / 1000),
      endAt: Math.floor(Date.UTC(2026, 4, 12, 10) / 1000),
    },
  ] as Parameters<typeof ShareMonthGrid>[0]['events'];

  return (
    <I18nextProvider i18n={instance}>
      <ShareMonthGrid events={events} timezone="UTC" holidaysCountry={holidaysCountry} />
    </I18nextProvider>
  );
}

describe('ShareMonthGrid holiday overlay', () => {
  it('draws the holidays of the advertised country onto the grid', () => {
    render(renderGrid('JP'));
    // 3 May 2026 is Constitution Memorial Day; the exact wording comes
    // from the shared provider, so the assertion is on the day being
    // annotated at all rather than on one label's phrasing.
    const cell = document.querySelector('[data-cell-key="2026-05-03"]');
    expect(cell).not.toBeNull();
    expect(cell?.textContent ?? '').not.toBe('3');
    expect((cell?.textContent ?? '').length).toBeGreaterThan(1);
  });

  it('leaves the grid unannotated when the share advertises no country', () => {
    render(renderGrid(null));
    const cell = document.querySelector('[data-cell-key="2026-05-03"]');
    expect(cell).not.toBeNull();
    expect(cell?.textContent).toBe('3');
  });

  it('does not fail the page when the country code is not a real region', () => {
    render(renderGrid('ZZ'));
    // The grid still renders; the overlay simply contributes nothing.
    const cell = document.querySelector('[data-cell-key="2026-05-03"]');
    expect(cell).not.toBeNull();
    expect(cell?.textContent).toBe('3');
  });
});
