/** The profile page picks the theme family and colour mode as two controls. */

import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';

const themeMock = vi.hoisted(() => ({
  family: 'glass',
  colorMode: 'system',
  setFamily: vi.fn(),
  setColorMode: vi.fn(),
}));

vi.mock('../../../providers/theme-provider', () => ({
  useTheme: () => themeMock,
}));

import ThemeAppearance from '../theme-appearance';

beforeEach(() => {
  themeMock.setFamily.mockReset();
  themeMock.setColorMode.mockReset();
});

function withI18n(children: ReactNode): ReactElement {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'auth',
      ns: ['auth'],
      resources: { en: { auth: enAuth } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return <I18nextProvider i18n={instance}>{children}</I18nextProvider>;
}

describe('ThemeAppearance', () => {
  it('renders a family group and a separate light / dark / system group', () => {
    render(withI18n(<ThemeAppearance />));

    const families = screen.getByRole('radiogroup', { name: enAuth.profile.theme });
    const modes = screen.getByRole('radiogroup', { name: enAuth.profile.color_mode });
    expect(families.querySelectorAll('[role="radio"]')).toHaveLength(5);
    expect(modes.querySelectorAll('[role="radio"]')).toHaveLength(3);
    expect(screen.getByRole('radio', { name: 'Glass' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByRole('radio', { name: 'System' }).getAttribute('aria-checked')).toBe('true');
  });

  it('changes the family without touching the colour mode, and vice versa', () => {
    render(withI18n(<ThemeAppearance />));

    fireEvent.click(screen.getByRole('radio', { name: /Washi/ }));
    expect(themeMock.setFamily).toHaveBeenCalledWith('washi');
    expect(themeMock.setColorMode).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('radio', { name: 'Dark' }));
    expect(themeMock.setColorMode).toHaveBeenCalledWith('dark');
    expect(themeMock.setFamily).toHaveBeenCalledTimes(1);
  });
});
