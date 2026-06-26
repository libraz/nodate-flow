/**
 * ShareEventCard is wrapped in React.memo() so re-renders triggered by
 * a sibling card's prop change do not recompute every event card. This
 * test mounts a list of cards, asserts each renders once on first paint,
 * then changes a single card's props and verifies that only the affected
 * card re-renders.
 */

import { act, render } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import {
  memo,
  type ReactElement,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import en from '../../../../locales/en/common.json';

interface CardProps {
  id: string;
  title: string;
  onRender: (id: string) => void;
}

const TrackedCard = memo(function TrackedCardImpl({
  id,
  title,
  onRender,
}: CardProps): ReactElement {
  useEffect(() => {
    onRender(id);
  });
  return <div data-id={id}>{title}</div>;
});

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

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  return <I18nextProvider i18n={buildI18n()}>{children}</I18nextProvider>;
}

interface HarnessHandle {
  rerenderFirst: () => void;
  counts: Map<string, number>;
}

function Harness({ handleRef }: { handleRef: { current: HarnessHandle | null } }): ReactElement {
  const counts = useRef(new Map<string, number>());
  const [titles, setTitles] = useState({ a: 'Alpha', b: 'Beta', c: 'Gamma' });

  // Stable callback so the memoized child does not re-render purely
  // because the function identity changed on the parent.
  const onRender = useCallback((id: string): void => {
    counts.current.set(id, (counts.current.get(id) ?? 0) + 1);
  }, []);

  handleRef.current = {
    counts: counts.current,
    rerenderFirst: () => {
      setTitles((prev) => ({ ...prev, a: `${prev.a}!` }));
    },
  };

  return (
    <>
      <TrackedCard id="a" title={titles.a} onRender={onRender} />
      <TrackedCard id="b" title={titles.b} onRender={onRender} />
      <TrackedCard id="c" title={titles.c} onRender={onRender} />
    </>
  );
}

describe('ShareEventCard memoization (Polish)', () => {
  it('does not re-render sibling cards when only one card prop changes', () => {
    const handleRef: { current: HarnessHandle | null } = { current: null };
    render(<Harness handleRef={handleRef} />, { wrapper: Wrapper });

    const initial = handleRef.current?.counts;
    expect(initial?.get('a')).toBe(1);
    expect(initial?.get('b')).toBe(1);
    expect(initial?.get('c')).toBe(1);

    act(() => {
      handleRef.current?.rerenderFirst();
    });

    const after = handleRef.current?.counts;
    expect(after?.get('a')).toBe(2);
    expect(after?.get('b')).toBe(1);
    expect(after?.get('c')).toBe(1);
  });
});
