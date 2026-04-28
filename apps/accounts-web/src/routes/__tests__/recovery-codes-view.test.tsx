/**
 * RecoveryCodesView — covers the Copy/Download/Print affordances added
 * for the recovery-codes save block. Clipboard is stubbed with a real
 * function so we can assert the success path without depending on a
 * happy-dom permission prompt; download is verified by spying on the
 * temporary anchor's `click`; print is verified by asserting the body
 * class is toggled around `window.print()`.
 */

import { fireEvent, render, screen } from '@testing-library/react';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enAuth from '../../../locales/en/auth.json';
import { RecoveryCodesView } from '../_authenticated/security';

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

const sampleCodes = ['ABCD-1234', 'EFGH-5678', 'IJKL-9012'];

describe('<RecoveryCodesView>', () => {
  beforeEach(() => {
    // Stub `URL.createObjectURL` / `revokeObjectURL` for happy-dom.
    if (!('createObjectURL' in URL)) {
      Object.defineProperty(URL, 'createObjectURL', { value: vi.fn(() => 'blob:mock') });
    }
    if (!('revokeObjectURL' in URL)) {
      Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn() });
    }
  });

  afterEach(() => {
    document.body.classList.remove('nf-print-recovery-codes');
    vi.restoreAllMocks();
  });

  it('renders the three save affordances (copy, download, print)', () => {
    render(<RecoveryCodesView codes={sampleCodes} onDismiss={vi.fn()} />, { wrapper: Wrapper });

    expect(screen.getByRole('button', { name: /copy all recovery codes/i })).toBeTruthy();
    expect(screen.getByRole('button', { name: /download recovery codes/i })).toBeTruthy();
    expect(screen.getByRole('button', { name: /print recovery codes/i })).toBeTruthy();
  });

  it('writes newline-separated codes to the clipboard on "Copy all"', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    render(<RecoveryCodesView codes={sampleCodes} onDismiss={vi.fn()} />, { wrapper: Wrapper });
    fireEvent.click(screen.getByRole('button', { name: /copy all recovery codes/i }));

    // Allow the awaited promise inside the handler to settle.
    await Promise.resolve();
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith(sampleCodes.join('\n'));
  });

  it('triggers a download click on "Download .txt"', () => {
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    render(<RecoveryCodesView codes={sampleCodes} onDismiss={vi.fn()} />, { wrapper: Wrapper });

    fireEvent.click(screen.getByRole('button', { name: /download recovery codes/i }));

    expect(clickSpy).toHaveBeenCalledTimes(1);
  });

  it('toggles the print body class while `window.print()` runs', () => {
    // happy-dom does not implement `window.print()` so we assign a stub
    // before spying. The stub captures whether the body class flag is
    // present at the moment the dialog would open.
    let classPresentDuringPrint = false;
    const printStub = vi.fn(() => {
      classPresentDuringPrint = document.body.classList.contains('nf-print-recovery-codes');
    });
    Object.defineProperty(window, 'print', { value: printStub, configurable: true });

    render(<RecoveryCodesView codes={sampleCodes} onDismiss={vi.fn()} />, { wrapper: Wrapper });
    fireEvent.click(screen.getByRole('button', { name: /print recovery codes/i }));

    expect(printStub).toHaveBeenCalledTimes(1);
    expect(classPresentDuringPrint).toBe(true);
    // The handler must restore the body class once printing is done.
    expect(document.body.classList.contains('nf-print-recovery-codes')).toBe(false);
  });
});
