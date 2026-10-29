/**
 * The list view's load-more sentinel sits below the grid, so it must be
 * observed against the ancestor that scrolls it. Observing it against the
 * grid (which does not contain it) meant the next page never auto-loaded.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const state = vi.hoisted(() => ({
  fetchNextPage: vi.fn(() => Promise.resolve()),
}));

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    useTasksInfiniteQuery: () => ({
      data: { pages: [{ tasks: [] }] },
      hasNextPage: true,
      isFetchingNextPage: false,
      fetchNextPage: state.fetchNextPage,
    }),
    useUpdateTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useDeleteTask: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useReorderTasks: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

vi.mock('../../projects/api', async () => {
  const actual = await vi.importActual<typeof import('../../projects/api')>('../../projects/api');
  return { ...actual, useProjectDependenciesQuery: () => ({ data: [] }) };
});

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => children,
  useNavigate: () => vi.fn(),
}));

import TaskListView from '../task-list-view';

interface Observed {
  root: Element | Document | null | undefined;
  callback: IntersectionObserverCallback;
}

let observed: Observed[] = [];

class FakeIntersectionObserver {
  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    observed.push({ root: options?.root, callback });
  }
  observe(): void {}
  disconnect(): void {}
  unobserve(): void {}
  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }
}

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: {} } },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  const client = new QueryClient();
  return (
    <I18nextProvider i18n={testI18n()}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nextProvider>
  );
}

describe('TaskListView infinite scroll', () => {
  beforeEach(() => {
    observed = [];
    state.fetchNextPage.mockClear();
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('observes the sentinel against its scrolling ancestor and loads the next page', () => {
    const { getByTestId } = render(
      <Wrapper>
        <div data-testid="scroller" style={{ overflowY: 'auto' }}>
          <TaskListView projectId="proj-001" />
        </div>
      </Wrapper>,
    );

    const last = observed.at(-1);
    expect(last?.root).toBe(getByTestId('scroller'));

    act(() => {
      last?.callback(
        [{ isIntersecting: true } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });
    expect(state.fetchNextPage).toHaveBeenCalledTimes(1);
  });
});
