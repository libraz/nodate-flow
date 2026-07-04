import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import ThemePicker, { DEFAULT_THEME_PREVIEWS, type ThemeFamilyEntry } from './theme-picker';

const THEMES: ThemeFamilyEntry[] = [
  { id: 'aurora', label: 'Aurora', preview: DEFAULT_THEME_PREVIEWS.aurora },
  { id: 'dotline', label: 'Dotline', preview: DEFAULT_THEME_PREVIEWS.dotline },
  { id: 'glass', label: 'Glass', preview: DEFAULT_THEME_PREVIEWS.glass },
];

describe('ThemePicker', () => {
  it('uses roving tabindex for theme and color-mode radio groups', () => {
    render(
      <ThemePicker
        themes={THEMES}
        selectedTheme="dotline"
        selectedColorMode="dark"
        onThemeChange={() => {}}
        onColorModeChange={() => {}}
      />,
    );

    expect(screen.getByRole('radio', { name: 'Aurora' }).getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('radio', { name: 'Dotline' }).getAttribute('tabindex')).toBe('0');
    expect(screen.getByRole('radio', { name: 'Light' }).getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('radio', { name: 'Dark' }).getAttribute('tabindex')).toBe('0');
  });

  it('changes theme selection with arrow keys', async () => {
    const user = userEvent.setup();
    const onThemeChange = vi.fn();
    render(
      <ThemePicker
        themes={THEMES}
        selectedTheme="aurora"
        selectedColorMode="light"
        onThemeChange={onThemeChange}
        onColorModeChange={() => {}}
      />,
    );

    screen.getByRole('radio', { name: 'Aurora' }).focus();
    await user.keyboard('{ArrowRight}');

    expect(onThemeChange).toHaveBeenCalledWith('dotline');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Dotline' }));
  });

  it('uses RTL-aware horizontal arrow navigation for color mode', async () => {
    const user = userEvent.setup();
    const onColorModeChange = vi.fn();
    render(
      <div dir="rtl">
        <ThemePicker
          themes={THEMES}
          selectedTheme="aurora"
          selectedColorMode="light"
          onThemeChange={() => {}}
          onColorModeChange={onColorModeChange}
        />
      </div>,
    );

    screen.getByRole('radio', { name: 'Light' }).focus();
    await user.keyboard('{ArrowRight}');

    expect(onColorModeChange).toHaveBeenCalledWith('system');
    expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'System' }));
  });
});
