/**
 * The public lens page is the one screen in the product a reader can land on
 * with no account and no session. It used to print the API's raw values: a
 * Japanese reader saw the English derived state ("waiting"), a bare priority
 * integer, and an ISO `YYYY-MM-DD` due date, on a page whose own chrome was
 * already translated.
 *
 * The assertions below use the real locale bundles rather than key echoes,
 * because the defect was that the values never entered the i18n path at all —
 * a test against `tasks.status.waiting` would pass on the broken version if
 * it stubbed the label maps.
 */

import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import enCommon from '../../../../locales/en/common.json';
import enSharing from '../../../../locales/en/sharing.json';
import jaCommon from '../../../../locales/ja/common.json';
import jaSharing from '../../../../locales/ja/sharing.json';

const queryMock = vi.hoisted(() => ({
  data: {
    name: 'Roadmap',
    tasks: [
      {
        id: '01920000-0000-7000-8000-000000000001',
        title: 'Ship the importer',
        status: 'waiting',
        priority: 3,
        dueOn: '2026-04-28',
        assigneeDisplayName: 'Aoi',
      },
      {
        id: '01920000-0000-7000-8000-000000000002',
        title: 'Draft the release note',
        status: 'done',
        priority: 0,
      },
    ],
  } as unknown,
}));

vi.mock('../api', () => ({
  usePublicLensQuery: () => ({ data: queryMock.data, isLoading: false, error: null }),
}));

import PublicLensPage from '../public-lens-page';

function renderIn(lng: 'en' | 'ja'): void {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng,
    fallbackLng: 'en',
    defaultNS: 'sharing',
    ns: ['sharing', 'common'],
    resources: {
      en: { sharing: enSharing, common: enCommon },
      ja: { sharing: jaSharing, common: jaCommon },
    },
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
  render(
    <I18nextProvider i18n={instance}>
      <PublicLensPage token="tok" />
    </I18nextProvider>,
  );
}

/** Text of the cells in the row whose first cell is `title`. */
function rowCells(title: string): string[] {
  const cell = screen.getByText(title);
  const row = cell.closest('tr');
  if (!row) throw new Error(`no row for ${title}`);
  return Array.from(row.querySelectorAll('td')).map((td) => td.textContent ?? '');
}

describe('PublicLensPage localization', () => {
  it('translates the derived state instead of printing the API enum', () => {
    renderIn('ja');
    expect(rowCells('Ship the importer')[1]).toBe('進行中');
    expect(rowCells('Draft the release note')[1]).toBe('完了');
    expect(screen.queryByText('waiting')).toBeNull();
  });

  it('names the priority instead of printing 0-4', () => {
    renderIn('ja');
    expect(rowCells('Ship the importer')[2]).toBe('高');
    expect(rowCells('Draft the release note')[2]).toBe('なし');
  });

  it('formats the due date for the reader locale', () => {
    renderIn('ja');
    const due = rowCells('Ship the importer')[3] ?? '';
    expect(due).not.toBe('2026-04-28');
    // Whatever Intl produces for ja, it has to still be the 28th of April.
    expect(due).toContain('2026');
    expect(due).toMatch(/4/);
    expect(due).toMatch(/28/);
  });

  it('shows the same three columns in English without falling back to raw values', () => {
    renderIn('en');
    const cells = rowCells('Ship the importer');
    expect(cells[1]).toBe('In progress');
    expect(cells[2]).toBe('High');
    expect(cells[3]).toBe('Apr 28, 2026');
  });

  it('leaves a placeholder for a task with no due date or assignee', () => {
    renderIn('ja');
    const cells = rowCells('Draft the release note');
    expect(cells[3]).toBe('—');
    expect(cells[4]).toBe('—');
  });

  it('falls back to the raw value for a state the label map does not know', () => {
    const previous = queryMock.data;
    queryMock.data = {
      name: 'Roadmap',
      tasks: [
        {
          id: '01920000-0000-7000-8000-000000000003',
          title: 'Unknown state',
          status: 'quantum',
          priority: 9,
        },
      ],
    };
    try {
      renderIn('ja');
      const cells = rowCells('Unknown state');
      expect(cells[1]).toBe('quantum');
      expect(cells[2]).toBe('9');
    } finally {
      queryMock.data = previous;
    }
  });
});
