/**
 * Theme family + colour mode controls for the profile page.
 *
 * Both delegate to the ThemeProvider, which persists locally and syncs the
 * preference to the server; they are not part of the profile form's save.
 */

import ThemePicker, {
  DEFAULT_THEME_PREVIEWS,
  type ThemeFamilyEntry,
} from '@nodate-flow/ui/primitives/theme-picker';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { useTheme } from '../../providers/theme-provider';

export default function ThemeAppearance(): ReactElement {
  const { t } = useTranslation('auth');
  const { family, colorMode, setFamily, setColorMode } = useTheme();

  const themeEntries: ThemeFamilyEntry[] = [
    { id: 'glass', label: t('profile.theme_glass'), preview: DEFAULT_THEME_PREVIEWS.glass },
    { id: 'modern', label: t('profile.theme_modern'), preview: DEFAULT_THEME_PREVIEWS.modern },
    { id: 'washi', label: t('profile.theme_washi'), preview: DEFAULT_THEME_PREVIEWS.washi },
    { id: 'sakura', label: t('profile.theme_sakura'), preview: DEFAULT_THEME_PREVIEWS.sakura },
    { id: 'classic', label: t('profile.theme_classic'), preview: DEFAULT_THEME_PREVIEWS.classic },
    { id: 'nothing', label: t('profile.theme_nothing'), preview: DEFAULT_THEME_PREVIEWS.nothing },
  ];

  const colorModeEntries = [
    { mode: 'light' as const, label: t('profile.color_mode_light') },
    { mode: 'dark' as const, label: t('profile.color_mode_dark') },
    { mode: 'system' as const, label: t('profile.color_mode_system') },
  ];

  return (
    <ThemePicker
      themes={themeEntries}
      selectedTheme={family}
      colorModes={colorModeEntries}
      selectedColorMode={colorMode}
      onThemeChange={setFamily}
      onColorModeChange={setColorMode}
      themeLabel={t('profile.theme')}
      colorModeLabel={t('profile.color_mode')}
    />
  );
}
