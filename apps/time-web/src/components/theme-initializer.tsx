import { useEffect, useRef } from 'react';

import { authApi } from '../lib/api-client';
import { applyColorMode, applyTheme, splitPreference, watchSystemColorScheme } from '../lib/theme';
import { useAuthStore } from '../stores/auth-store';
import { useCalendarUiStore } from '../stores/calendar-ui-store';

/** ThemeInitializer applies the active theme to the DOM and hydrates from the server on auth. */
export default function ThemeInitializer(): null {
  const theme = useCalendarUiStore((s) => s.theme);
  const colorMode = useCalendarUiStore((s) => s.colorMode);
  const setTheme = useCalendarUiStore((s) => s.setTheme);
  const setColorMode = useCalendarUiStore((s) => s.setColorMode);
  const hydratedRef = useRef(false);

  // Apply theme on mount and changes
  useEffect(() => {
    applyTheme(theme, colorMode);
    applyColorMode(colorMode);
  }, [theme, colorMode]);

  // Watch system preference changes
  useEffect(() => {
    if (colorMode !== 'system') return;
    return watchSystemColorScheme(() => {
      applyTheme(theme, 'system');
      applyColorMode('system');
    });
  }, [colorMode, theme]);

  // One-shot hydration from server when authenticated
  useEffect(() => {
    if (hydratedRef.current) return;
    const tryHydrate = (): void => {
      if (hydratedRef.current) return;
      const token = useAuthStore.getState().accessToken;
      if (!token) return;
      hydratedRef.current = true;
      void authApi.me().then((me) => {
        if (me.themePreference) {
          const { theme: serverTheme, colorMode: serverMode } = splitPreference(me.themePreference);
          setTheme(serverTheme);
          setColorMode(serverMode);
        }
      });
    };
    tryHydrate();
    const unsub = useAuthStore.subscribe(tryHydrate);
    return () => {
      unsub();
    };
  }, [setTheme, setColorMode]);

  return null;
}
