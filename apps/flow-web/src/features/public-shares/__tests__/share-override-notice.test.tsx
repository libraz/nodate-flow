import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      [key, ...Object.values(params ?? {})].join(' '),
  }),
}));

import ShareOverrideNotice from '../share-override-notice';

describe('<ShareOverrideNotice> for an all-day series', () => {
  const originalTz = process.env.TZ;
  beforeEach(() => {
    process.env.TZ = 'America/Los_Angeles';
  });
  afterEach(() => {
    if (originalTz === undefined) delete process.env.TZ;
    else process.env.TZ = originalTz;
  });

  it('names the UTC dates, whatever zone the browser is in', () => {
    render(
      <table>
        <tbody>
          <ShareOverrideNotice
            warnings={[
              {
                eventId: 'e1',
                originalStart: Date.UTC(2026, 2, 9) / 1000,
                startAt: Date.UTC(2026, 2, 10) / 1000,
                visibility: 'default',
              } as never,
            ]}
            allDay
            locale="en"
            columnCount={3}
            busy={false}
            rowStyle={{}}
            onAttach={() => {}}
          />
        </tbody>
      </table>,
    );
    expect(screen.getByText(/Mar 9, 2026 Mar 10, 2026/)).toBeDefined();
  });
});
