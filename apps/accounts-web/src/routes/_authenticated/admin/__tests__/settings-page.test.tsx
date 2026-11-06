import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import enAdmin from '../../../../../locales/en/admin.json';
import type { CapabilitiesState } from '../../../../features/auth/use-capabilities';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  patch: vi.fn(),
}));

const capsMock = vi.hoisted(() => ({ state: { status: 'loading' } as CapabilitiesState }));

vi.mock('../../../../lib/sdk', () => ({
  sdk: { GET: sdkMocks.get, PATCH: sdkMocks.patch },
  authSdk: { GET: sdkMocks.get, PATCH: sdkMocks.patch },
}));

vi.mock('../../../../features/auth/use-capabilities', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('../../../../features/auth/use-capabilities')>();
  return { ...actual, useCapabilities: () => capsMock.state };
});

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

const { SettingsPage } = await import('../settings');

function mount(state: CapabilitiesState): void {
  capsMock.state = state;
  const testI18n = i18n.createInstance();
  void testI18n
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: { en: { admin: enAdmin } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  render(
    <I18nextProvider i18n={testI18n}>
      <SettingsPage />
    </I18nextProvider>,
  );
}

function readyState(registrationOpen: boolean): CapabilitiesState {
  return {
    status: 'ready',
    caps: { registrationOpen } as Extract<CapabilitiesState, { status: 'ready' }>['caps'],
  };
}

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.patch.mockReset();
});

function expectReadOnly(): void {
  expect(document.querySelector('form')).toBeNull();
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.queryByRole('textbox')).toBeNull();
  expect(screen.queryByRole('combobox')).toBeNull();
  expect(sdkMocks.patch).not.toHaveBeenCalled();
  expect(sdkMocks.get).not.toHaveBeenCalled();
}

describe('admin settings page', () => {
  it('shows registration as open, read-only', () => {
    mount(readyState(true));
    expect(screen.getByRole('heading', { name: enAdmin.settings.title })).toBeTruthy();
    expect(screen.getByText(enAdmin.settings.description)).toBeTruthy();
    expect(screen.getByText(enAdmin.settings.registration_open)).toBeTruthy();
    expect(screen.getByText(enAdmin.settings.registration_state_open)).toBeTruthy();
    expect(screen.queryByText(enAdmin.settings.registration_state_closed)).toBeNull();
    expect(screen.getByText(enAdmin.settings.registration_open_desc)).toBeTruthy();
    expectReadOnly();
  });

  it('shows registration as closed, read-only', () => {
    mount(readyState(false));
    expect(screen.getByText(enAdmin.settings.registration_state_closed)).toBeTruthy();
    expect(screen.queryByText(enAdmin.settings.registration_state_open)).toBeNull();
    expectReadOnly();
  });

  it('shows a loading placeholder without values', () => {
    mount({ status: 'loading' });
    expect(screen.getByText(enAdmin.common.loading)).toBeTruthy();
    expect(screen.queryByText(enAdmin.settings.registration_state_open)).toBeNull();
    expect(screen.queryByText(enAdmin.settings.registration_state_closed)).toBeNull();
    expectReadOnly();
  });

  it('shows an alert when capabilities fail to load', () => {
    mount({ status: 'error', retry: vi.fn() });
    expect(screen.getByRole('alert').textContent).toBe(enAdmin.settings.unavailable);
    expect(screen.queryByText(enAdmin.settings.registration_open)).toBeNull();
    expectReadOnly();
  });
});
