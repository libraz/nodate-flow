/** The profile page picks the theme family and colour mode as two controls. */

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

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
import { authResources } from './new-auth-keys';
import { withI18n } from './test-i18n';

beforeEach(() => {
  themeMock.setFamily.mockReset();
  themeMock.setColorMode.mockReset();
});

describe('ThemeAppearance', () => {
  it('renders a family group and a separate light / dark / system group', () => {
    render(withI18n(<ThemeAppearance />));

    const families = screen.getByRole('radiogroup', { name: authResources.profile.theme });
    const modes = screen.getByRole('radiogroup', { name: authResources.profile.color_mode });
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
