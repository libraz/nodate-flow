/**
 * PublicShareCalPage renders the holidays-overlay label by resolving the
 * stored ISO country code (e.g. "JP") to a locale-aware display name (e.g.
 * "Japan") via Intl.DisplayNames, mirroring the authenticated holidays
 * surface. This regression guards against printing the raw code on the
 * public, unauthenticated share page.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, describe, expect, it, vi } from 'vitest';

import en from '../../../../locales/en/common.json';
import type { NormalisedShareRender } from '../use-share-render-query';

// Pass-through stubs so the test exercises only the page's own header
// rendering, not the month grid or the shared public chrome.
vi.mock('../share-month-grid', () => ({
  default: (): ReactElement => <div data-testid="share-month-grid" />,
}));
vi.mock('../../../components/public-page-layout', () => ({
  default: ({
    beforeMain,
    children,
  }: {
    beforeMain?: ReactNode;
    children: ReactNode;
  }): ReactElement => (
    <div>
      {beforeMain}
      {children}
    </div>
  ),
}));

interface ShareRenderResult {
  data: NormalisedShareRender | undefined;
  isLoading: boolean;
  error: unknown;
}

const mockShareRender = vi.fn<() => ShareRenderResult>();

vi.mock('../use-share-render-query', () => ({
  useShareRenderQuery: () => mockShareRender(),
}));

import PublicShareCalPage from '../public-share-cal-page';

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'common',
      ns: ['common'],
      resources: { en: { common: en } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

function renderPage(): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, throwOnError: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={buildI18n()}>
        <PublicShareCalPage token="tok-123" />
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

function pageWith(showHolidaysCountry: string | undefined): NormalisedShareRender {
  return {
    page: {
      createdAt: 0,
      timezone: 'Asia/Tokyo',
      title: 'Team calendar',
      workspaceId: 'ws-1',
      workspaceName: 'Acme',
      ...(showHolidaysCountry !== undefined ? { showHolidaysCountry } : {}),
    },
    events: [],
  };
}

describe('<PublicShareCalPage> holidays country label', () => {
  afterEach(() => {
    mockShareRender.mockReset();
  });

  it('resolves the ISO country code to a localized display name', () => {
    mockShareRender.mockReturnValue({
      data: pageWith('JP'),
      isLoading: false,
      error: null,
    });

    renderPage();

    // getByText throws if the localized label is absent.
    expect(screen.getByText('Holidays: Japan')).toBeTruthy();
    expect(screen.queryByText('Holidays: JP')).toBeNull();
  });

  it('falls back to the raw code when resolution throws', () => {
    mockShareRender.mockReturnValue({
      // "Q" is not a well-formed region subtag, so Intl.DisplayNames.of
      // throws a RangeError and the catch branch returns the raw code.
      data: pageWith('Q'),
      isLoading: false,
      error: null,
    });

    renderPage();

    expect(screen.getByText('Holidays: Q')).toBeTruthy();
  });

  it('omits the holidays label entirely when no country is configured', () => {
    mockShareRender.mockReturnValue({
      data: pageWith(undefined),
      isLoading: false,
      error: null,
    });

    renderPage();

    expect(screen.queryByText(/^Holidays:/)).toBeNull();
  });
});
