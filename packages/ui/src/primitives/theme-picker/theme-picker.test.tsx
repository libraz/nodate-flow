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

  /*
   * A selection that names something not on offer used to take both
   * radiogroups out of the tab order entirely: every entry was
   * `tabIndex=-1`, so Tab walked straight past a control that looked
   * perfectly normal on screen. These tests reach the groups by tabbing
   * rather than by reading the attribute, because the attribute is only
   * interesting insofar as it makes the control reachable.
   */
  describe('when the selection matches nothing on offer', () => {
    function renderOrphanSelection(onThemeChange = vi.fn()): void {
      render(
        <ThemePicker
          themes={THEMES.slice(0, 2)}
          // A theme family the host app no longer lists — e.g. restored
          // from a profile written by an older build.
          selectedTheme="glass"
          selectedColorMode={'sepia' as never}
          onThemeChange={onThemeChange}
          onColorModeChange={() => {}}
        />,
      );
    }

    it('still lets Tab reach the theme group', async () => {
      const user = userEvent.setup();
      renderOrphanSelection();

      await user.tab();
      expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Aurora' }));
    });

    it('still lets Tab reach the color mode group', async () => {
      const user = userEvent.setup();
      renderOrphanSelection();

      await user.tab();
      await user.tab();
      expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Light' }));
    });

    it('still lets the reachable entry be operated', async () => {
      const user = userEvent.setup();
      const onThemeChange = vi.fn();
      renderOrphanSelection(onThemeChange);

      await user.tab();
      await user.keyboard('{ArrowRight}');
      expect(onThemeChange).toHaveBeenCalledWith('dotline');
      expect(document.activeElement).toBe(screen.getByRole('radio', { name: 'Dotline' }));
    });

    it('checks none of the entries, so the fallback is not a silent selection', () => {
      renderOrphanSelection();
      for (const radio of screen.getAllByRole('radio')) {
        expect(radio.getAttribute('aria-checked')).toBe('false');
      }
    });
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
