import { Globe } from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import Dialog from '@nodate-flow/ui/primitives/dialog';
import ThemePicker, {
  DEFAULT_THEME_PREVIEWS,
  type ThemeFamilyEntry,
} from '@nodate-flow/ui/primitives/theme-picker';
import type { ColorMode, ThemeFamily } from '@nodate-flow/ui/providers/theme-provider';

import { type SupportedLanguage, i18n, setLanguage } from '../../i18n';
import type { Theme } from '../../lib/theme';
import { useCalendarUi } from '../../stores/calendar-ui-store';

const LANGUAGES: { value: SupportedLanguage; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'ja', label: '\u65E5\u672C\u8A9E' },
];

export default function SettingsModal(): ReactElement | null {
  const { t } = useTranslation();
  const showSettings = useCalendarUi((s) => s.showSettings);
  const toggleSettings = useCalendarUi((s) => s.toggleSettings);
  const theme = useCalendarUi((s) => s.theme);
  const colorMode = useCalendarUi((s) => s.colorMode);
  const setTheme = useCalendarUi((s) => s.setTheme);
  const setColorMode = useCalendarUi((s) => s.setColorMode);

  const currentLang = (i18n.language?.substring(0, 2) ?? 'en') as SupportedLanguage;

  const themes: ThemeFamilyEntry[] = [
    {
      id: 'glass' as ThemeFamily,
      label: t('settings.theme_glass'),
      preview: DEFAULT_THEME_PREVIEWS.glass,
    },
    {
      id: 'aurora' as ThemeFamily,
      label: t('settings.theme_aurora'),
      preview: DEFAULT_THEME_PREVIEWS.aurora,
    },
    {
      id: 'dotline' as ThemeFamily,
      label: t('settings.theme_dotline'),
      preview: DEFAULT_THEME_PREVIEWS.dotline,
    },
  ];

  const colorModes = [
    { mode: 'light' as ColorMode, label: t('settings.mode_light') },
    { mode: 'dark' as ColorMode, label: t('settings.mode_dark') },
    { mode: 'system' as ColorMode, label: t('settings.mode_system') },
  ];

  if (!showSettings) return null;

  return (
    <Dialog
      open={showSettings}
      onClose={toggleSettings}
      title={t('settings.title')}
      fullScreenOnMobile
      style={{ maxInlineSize: '26rem' }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}>
        <ThemePicker
          themes={themes}
          selectedTheme={theme as ThemeFamily}
          colorModes={colorModes}
          selectedColorMode={colorMode as ColorMode}
          onThemeChange={(t) => setTheme(t as Theme)}
          onColorModeChange={(m) => setColorMode(m as ColorMode)}
          themeLabel={t('settings.theme')}
          colorModeLabel={t('settings.color_mode')}
        />

        {/* Language */}
        <div>
          <span
            style={{
              display: 'block',
              marginBlockEnd: 'var(--nf-space-2)',
              fontSize: 'var(--nf-text-xs)',
              fontWeight: 'var(--nf-weight-medium)',
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              color: 'var(--nf-color-fg-subtle)',
            }}
          >
            {t('settings.language')}
          </span>
          <div style={{ display: 'flex', gap: 'var(--nf-space-2)' }}>
            {LANGUAGES.map((lang) => {
              const selected = currentLang === lang.value;
              return (
                <button
                  key={lang.value}
                  type="button"
                  onClick={() => setLanguage(lang.value)}
                  style={{
                    all: 'unset',
                    cursor: 'pointer',
                    flex: 1,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: 'var(--nf-space-2)',
                    padding: 'var(--nf-space-2) 0',
                    borderRadius: 'var(--nf-radius-md)',
                    border: selected
                      ? '2px solid var(--nf-color-accent)'
                      : '2px solid var(--nf-color-border)',
                    backgroundColor: selected ? 'var(--nf-color-accent-subtle)' : 'transparent',
                    transition: 'background var(--nf-duration-fast) var(--nf-ease-standard)',
                  }}
                >
                  <Globe
                    size={16}
                    style={{
                      color: selected ? 'var(--nf-color-accent)' : 'var(--nf-color-fg-subtle)',
                    }}
                  />
                  <span
                    style={{
                      fontSize: 'var(--nf-text-xs)',
                      fontWeight: 'var(--nf-weight-medium)',
                      color: selected ? 'var(--nf-color-accent)' : 'var(--nf-color-fg-muted)',
                    }}
                  >
                    {lang.label}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      </div>
    </Dialog>
  );
}
