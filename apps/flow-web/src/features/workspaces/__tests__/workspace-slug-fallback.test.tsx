/**
 * A Japanese-only workspace name derives no slug; submitting must fall back
 * to a random `workspace-xxxxxx` slug instead of failing slug validation,
 * and the slug field stays behind the collapsed advanced section.
 */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import type { ReactElement } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  mutateAsync: vi.fn<(input: { name: string; slug: string }) => Promise<unknown>>(),
  navigate: vi.fn(async () => undefined),
}));

vi.mock('../api', () => ({
  useCreateWorkspace: () => ({ mutateAsync: mocks.mutateAsync }),
}));

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock('../../../components/page-help', () => ({ default: () => null }));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: vi.fn() },
}));

import SetupPage from '../setup-page';
import WorkspaceCreateDialog from '../workspace-create-dialog';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'common',
    ns: ['common'],
    resources: { en: { common: {} } },
    interpolation: { escapeValue: false },
    parseMissingKeyHandler: (key: string) => key,
    react: { useSuspense: false },
  });
  return instance;
}

function renderWithI18n(ui: ReactElement): void {
  render(<I18nextProvider i18n={testI18n()}>{ui}</I18nextProvider>);
}

beforeEach(() => {
  mocks.mutateAsync.mockReset();
  mocks.mutateAsync.mockResolvedValue({ id: 'ws-1' });
});

const cases: Array<[string, () => ReactElement, string]> = [
  ['SetupPage', () => <SetupPage />, 'workspaces.setup.submit'],
  [
    'WorkspaceCreateDialog',
    () => <WorkspaceCreateDialog open onClose={() => undefined} />,
    'workspaces.form.submit',
  ],
];

describe.each(cases)('%s slug fallback', (_label, element, submitKey) => {
  it('hides the slug field until the advanced section is opened', async () => {
    const user = userEvent.setup();
    renderWithI18n(element());

    const details = screen.getByLabelText('workspaces.form.slug').closest('details');
    expect(details?.open).toBe(false);
    await user.click(screen.getByText('workspaces.form.advanced'));
    await waitFor(() => {
      expect(details?.open).toBe(true);
    });
  });

  it('submits a random workspace slug for a Japanese-only name', async () => {
    const user = userEvent.setup();
    renderWithI18n(element());

    await user.type(screen.getByLabelText(/workspaces\.form\.name/), '営業チーム');
    await user.click(screen.getByRole('button', { name: submitKey }));

    await waitFor(() => {
      expect(mocks.mutateAsync).toHaveBeenCalledTimes(1);
    });
    const input = mocks.mutateAsync.mock.calls[0]?.[0];
    expect(input?.name).toBe('営業チーム');
    expect(input?.slug).toMatch(/^workspace-[a-z0-9]{6}$/);
    expect(screen.queryByText('workspaces.validation.slug_format')).toBeNull();
    expect(screen.queryByText('workspaces.validation.slug_required')).toBeNull();
  });
});
