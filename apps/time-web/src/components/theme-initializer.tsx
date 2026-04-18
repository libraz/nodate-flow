import { useEffect } from 'react';

import { applyColorMode, applyTheme, watchSystemColorScheme } from '../lib/theme';
import { useCalendarUiStore } from '../stores/calendar-ui-store';

export default function ThemeInitializer(): null {
  const theme = useCalendarUiStore((s) => s.theme);
  const colorMode = useCalendarUiStore((s) => s.colorMode);
  useEffect(() => {
    applyTheme(theme, colorMode);
    applyColorMode(colorMode);
  }, [theme, colorMode]);

  useEffect(() => {
    if (colorMode !== 'system') return;
    return watchSystemColorScheme(() => {
      applyTheme(theme, 'system');
      applyColorMode('system');
    });
  }, [colorMode, theme]);

  return null;
}
