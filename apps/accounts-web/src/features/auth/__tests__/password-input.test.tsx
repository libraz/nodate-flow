/**
 * PasswordInput — show/hide toggle and CapsLock indicator behaviour.
 */

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import enAuth from '../../../../locales/en/auth.json';
import PasswordInput from '../password-input';

function buildI18n(): ReturnType<typeof i18n.createInstance> {
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
  return instance;
}

function Wrapper({ children }: { children: ReactNode }): ReactElement {
  return <I18nextProvider i18n={buildI18n()}>{children}</I18nextProvider>;
}

describe('<PasswordInput>', () => {
  it('renders as type=password by default and toggles to type=text when the toggle is pressed', async () => {
    render(<PasswordInput aria-label="password" />, { wrapper: Wrapper });

    const input = screen.getByLabelText('password') as HTMLInputElement;
    expect(input.type).toBe('password');

    const toggle = screen.getByRole('button', { name: enAuth.login.password_show });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');

    await userEvent.click(toggle);

    expect(input.type).toBe('text');
    const hideToggle = screen.getByRole('button', { name: enAuth.login.password_hide });
    expect(hideToggle.getAttribute('aria-pressed')).toBe('true');

    await userEvent.click(hideToggle);
    expect(input.type).toBe('password');
  });

  it('shows a CapsLock-on hint while the field is focused and CapsLock is engaged', () => {
    const original = KeyboardEvent.prototype.getModifierState;
    let capsState = true;
    KeyboardEvent.prototype.getModifierState = (key: string): boolean =>
      key === 'CapsLock' ? capsState : false;
    try {
      render(<PasswordInput aria-label="password" />, { wrapper: Wrapper });

      const input = screen.getByLabelText('password') as HTMLInputElement;
      fireEvent.focus(input);
      fireEvent.keyDown(input, { key: 'A' });
      expect(screen.getByText(enAuth.login.caps_lock_on)).toBeDefined();

      capsState = false;
      fireEvent.keyDown(input, { key: 'A' });
      expect(screen.queryByText(enAuth.login.caps_lock_on)).toBeNull();
    } finally {
      KeyboardEvent.prototype.getModifierState = original;
    }
  });

  it('clears the CapsLock hint when the field loses focus', () => {
    const original = KeyboardEvent.prototype.getModifierState;
    KeyboardEvent.prototype.getModifierState = (): boolean => true;
    try {
      render(<PasswordInput aria-label="password" />, { wrapper: Wrapper });

      const input = screen.getByLabelText('password') as HTMLInputElement;
      fireEvent.focus(input);
      fireEvent.keyDown(input, { key: 'A' });
      expect(screen.getByText(enAuth.login.caps_lock_on)).toBeDefined();

      fireEvent.blur(input);
      expect(screen.queryByText(enAuth.login.caps_lock_on)).toBeNull();
    } finally {
      KeyboardEvent.prototype.getModifierState = original;
    }
  });
});
