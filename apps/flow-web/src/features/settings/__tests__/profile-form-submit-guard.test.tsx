/**
 * The profile-form save button must stay disabled while either the
 * react-hook-form `isSubmitting` flag OR the underlying useUpdateMe
 * mutation `isPending` flag is true. The previous code only gated on
 * `isSubmitting`, so a slow PATCH whose handler had already returned
 * (because the await resolved) re-enabled the button before the
 * mutation actually settled. A second click would queue a duplicate
 * PATCH.
 *
 * These are behavioral assertions against the rendered button, not a
 * source-text check: each case drives `useUpdateMe`/form submission
 * into one of the two "in flight" states and asserts the button is
 * disabled and shows the saving label, then asserts the button is
 * enabled and shows the normal label once both flags are false.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Me, PatchMeInput } from '../api';

const apiMocks = vi.hoisted(() => ({
  me: {
    displayName: 'Jane Doe',
    locale: 'en',
    timezone: 'UTC',
    country: 'US',
    weekStart: 'sun',
  } as Me,
  isPending: false,
  mutateAsync: vi.fn<(input: PatchMeInput) => Promise<Me>>(),
}));

vi.mock('../api', () => ({
  useMeQuery: () => ({ data: apiMocks.me }),
  useUpdateMe: () => ({
    isPending: apiMocks.isPending,
    mutateAsync: apiMocks.mutateAsync,
  }),
}));

vi.mock('../avatar-upload', () => ({
  default: () => null,
}));

vi.mock('../../../providers/theme-provider', () => ({
  useTheme: () => ({
    family: 'glass',
    colorMode: 'light',
    setFamily: vi.fn(),
    setColorMode: vi.fn(),
  }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

import ProfileForm from '../profile-form';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'settings',
    ns: ['settings'],
    resources: { en: { settings: {} } },
    interpolation: { escapeValue: false },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <I18nextProvider i18n={testI18n()}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nextProvider>
  );
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: /profile\.(save|saving)/ }) as HTMLButtonElement;
}

describe('profile-form save button submit guard', () => {
  afterEach(() => {
    vi.clearAllMocks();
    apiMocks.isPending = false;
  });

  it('enables the button and shows the save label when neither flag is set', () => {
    render(
      <Wrapper>
        <ProfileForm />
      </Wrapper>,
    );

    const button = saveButton();
    expect(button.disabled).toBe(false);
    expect(button.textContent).toBe('profile.save');
  });

  it('disables the button and shows the saving label while update.isPending is true, even though the form was never submitted', () => {
    apiMocks.isPending = true;

    render(
      <Wrapper>
        <ProfileForm />
      </Wrapper>,
    );

    // isSubmitting is false here (nothing was ever submitted) — this
    // isolates the update.isPending half of the OR. A regression to the
    // old `disabled={isSubmitting}` expression would render this
    // enabled, reproducing the double-submit bug.
    const button = saveButton();
    expect(button.disabled).toBe(true);
    expect(button.textContent).toBe('profile.saving');
  });

  it('disables the button and shows the saving label while the submit handler is still in flight, even though update.isPending never flips', async () => {
    // update.isPending stays false for the whole test: the mutation is
    // mocked out entirely, so only react-hook-form's isSubmitting flag
    // can be driving the disabled state here.
    let resolveMutation: ((me: Me) => void) | undefined;
    apiMocks.mutateAsync.mockImplementation(
      () =>
        new Promise<Me>((resolve) => {
          resolveMutation = resolve;
        }),
    );

    const user = userEvent.setup();
    render(
      <Wrapper>
        <ProfileForm />
      </Wrapper>,
    );

    await user.click(saveButton());

    await waitFor(() => {
      expect(saveButton().disabled).toBe(true);
    });
    expect(saveButton().textContent).toBe('profile.saving');

    // Let the in-flight submission settle so the test does not leak a
    // dangling timer/promise into the next case.
    resolveMutation?.(apiMocks.me);
    await waitFor(() => {
      expect(apiMocks.mutateAsync).toHaveBeenCalled();
    });
  });
});
