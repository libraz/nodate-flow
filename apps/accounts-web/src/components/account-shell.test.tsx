/**
 * AccountShell — sidebar wordmark follows the deployment brand.
 */

import { DEFAULT_APP_NAME } from '@nodate-flow/ui/brand';
import { render, screen } from '@testing-library/react';
import type { ReactElement, ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AuthUser } from '../features/auth/auth-store';

const brand = vi.hoisted(() => ({
  APP_NAME: 'nodate-flow',
  APP_LOGO_URL: undefined as string | undefined,
}));

vi.mock('@/lib/brand', () => brand);
vi.mock('../lib/brand', () => brand);
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }): ReactElement => <a href="/">{children}</a>,
}));

import AccountShell from './account-shell';

const user = {
  displayName: 'Ada',
  email: 'ada@example.com',
  isInstanceAdmin: false,
} as AuthUser;

describe('AccountShell brand', () => {
  beforeEach(() => {
    brand.APP_NAME = DEFAULT_APP_NAME;
    brand.APP_LOGO_URL = undefined;
  });

  it('shows the default name without a logo', () => {
    const { container } = render(<AccountShell user={user}>x</AccountShell>);
    expect(screen.getByText(DEFAULT_APP_NAME)).toBeTruthy();
    expect(container.querySelector('img[src]')).toBeNull();
  });

  it('shows the configured name and a decorative logo', () => {
    brand.APP_NAME = 'Acme Tasks';
    brand.APP_LOGO_URL = 'https://example.com/logo.svg';
    const { container } = render(<AccountShell user={user}>x</AccountShell>);
    expect(screen.getByText('Acme Tasks')).toBeTruthy();
    const img = container.querySelector('img[src="https://example.com/logo.svg"]');
    expect(img?.getAttribute('alt')).toBe('');
  });
});
