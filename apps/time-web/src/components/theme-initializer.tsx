import { useEffect, useRef } from 'react';

import { authSdk } from '../lib/sdk';
import { applyColorMode, applyTheme, splitPreference, watchSystemColorScheme } from '../lib/theme';
import { authStore } from '../stores/auth-store';
import { useCalendarUi } from '../stores/calendar-ui-store';

/** ThemeInitializer applies the active theme to the DOM and hydrates from the server on auth. */
export default function ThemeInitializer(): null {
  const theme = useCalendarUi((s) => s.theme);
  const colorMode = useCalendarUi((s) => s.colorMode);
  const setTheme = useCalendarUi((s) => s.setTheme);
  const setColorMode = useCalendarUi((s) => s.setColorMode);
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
      const token = authStore.getState().accessToken;
      if (!token) return;
      hydratedRef.current = true;
      void authSdk.GET('/auth/me').then((res) => {
        const me = res.data as { themePreference?: string } | undefined;
        if (me?.themePreference) {
          const { theme: serverTheme, colorMode: serverMode } = splitPreference(me.themePreference);
          setTheme(serverTheme);
          setColorMode(serverMode);
        }
      });
    };
    tryHydrate();
    const unsub = authStore.subscribe(tryHydrate);
    return () => {
      unsub();
    };
  }, [setTheme, setColorMode]);

  return null;
}
