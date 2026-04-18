import { Globe, Monitor, Moon, Sun, X } from 'lucide-react';
import { type ReactElement, useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import { type SupportedLanguage, i18n, setLanguage } from '../../i18n';
import type { ColorMode, Theme } from '../../lib/theme';
import { useCalendarUiStore } from '../../stores/calendar-ui-store';

const LANGUAGES: { value: SupportedLanguage; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'ja', label: '\u65E5\u672C\u8A9E' },
];

/** Visual theme preview cards */
function ThemeCard({
  themeId,
  label,
  selected,
  onSelect,
}: {
  themeId: Theme;
  label: string;
  selected: boolean;
  onSelect: () => void;
}): ReactElement {
  const previewStyles: Record<
    Theme,
    { bg: string; accent: string; radius: string; extra?: string }
  > = {
    glass: {
      bg: 'linear-gradient(135deg, rgba(200,220,255,0.4), rgba(255,200,255,0.3))',
      accent: '#007aff',
      radius: '12px',
    },
    aurora: { bg: 'linear-gradient(135deg, #e8f5e9, #e0f2f1)', accent: '#2ecc87', radius: '16px' },
    dotline: { bg: '#f5f5f5', accent: '#ef4444', radius: '0' },
  };

  const s = previewStyles[themeId];

  return (
    <button
      type="button"
      onClick={onSelect}
      className="group relative flex flex-col items-center gap-2 rounded-[var(--radius-md)] p-3 transition-all"
      style={{
        border: selected ? '2px solid var(--color-accent)' : '2px solid var(--color-border)',
        backgroundColor: selected ? 'var(--color-accent-bg)' : 'transparent',
      }}
    >
      {/* Mini preview */}
      <div
        className="flex h-[52px] w-full items-end overflow-hidden"
        style={{ background: s.bg, borderRadius: s.radius }}
      >
        <div className="flex w-full gap-1 p-1.5">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="h-2"
              style={{
                flex: i === 2 ? 2 : 1,
                backgroundColor: i === 1 ? s.accent : 'rgba(0,0,0,0.1)',
                borderRadius: s.radius === '0' ? '0' : '4px',
              }}
            />
          ))}
        </div>
      </div>
      <span
        className="text-[12px] font-medium"
        style={{ color: selected ? 'var(--color-accent)' : 'var(--color-text-secondary)' }}
      >
        {label}
      </span>
    </button>
  );
}

/** Color mode selector with icons */
function ColorModeButton({
  mode,
  label,
  selected,
  onSelect,
}: {
  mode: ColorMode;
  label: string;
  selected: boolean;
  onSelect: () => void;
}): ReactElement {
  const Icon = mode === 'light' ? Sun : mode === 'dark' ? Moon : Monitor;
  return (
    <button
      type="button"
      onClick={onSelect}
      className="flex flex-1 flex-col items-center gap-1.5 rounded-[var(--radius-md)] py-2.5 transition-all"
      style={{
        border: selected ? '2px solid var(--color-accent)' : '2px solid var(--color-border)',
        backgroundColor: selected ? 'var(--color-accent-bg)' : 'transparent',
      }}
    >
      <Icon
        className="h-5 w-5"
        style={{ color: selected ? 'var(--color-accent)' : 'var(--color-text-tertiary)' }}
      />
      <span
        className="text-[12px] font-medium"
        style={{ color: selected ? 'var(--color-accent)' : 'var(--color-text-secondary)' }}
      >
        {label}
      </span>
    </button>
  );
}

export default function SettingsModal(): ReactElement | null {
  const { t } = useTranslation();
  const showSettings = useCalendarUiStore((s) => s.showSettings);
  const toggleSettings = useCalendarUiStore((s) => s.toggleSettings);
  const theme = useCalendarUiStore((s) => s.theme);
  const colorMode = useCalendarUiStore((s) => s.colorMode);
  const setTheme = useCalendarUiStore((s) => s.setTheme);
  const setColorMode = useCalendarUiStore((s) => s.setColorMode);

  const currentLang = (i18n.language?.substring(0, 2) ?? 'en') as SupportedLanguage;

  const themes: { value: Theme; label: string }[] = [
    { value: 'glass', label: t('settings.themeGlass') },
    { value: 'aurora', label: t('settings.themeAurora') },
    { value: 'dotline', label: t('settings.themeDotline') },
  ];

  const colorModes: { value: ColorMode; label: string }[] = [
    { value: 'light', label: t('settings.modeLight') },
    { value: 'dark', label: t('settings.modeDark') },
    { value: 'system', label: t('settings.modeSystem') },
  ];

  const handleBackdropClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) toggleSettings();
    },
    [toggleSettings],
  );

  if (!showSettings) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--color-overlay)]"
      onClick={handleBackdropClick}
      onKeyDown={undefined}
      role="presentation"
    >
      <div className="glass-surface-heavy w-full max-w-[420px] rounded-[var(--radius-xl)] ring-1 ring-[var(--color-border)]">
        {/* Header */}
        <div className="flex items-center justify-between px-6 pt-5 pb-1">
          <h2 className="text-[18px] font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            {t('settings.title')}
          </h2>
          <button
            type="button"
            onClick={toggleSettings}
            className="flex h-8 w-8 items-center justify-center rounded-[var(--radius-sm)] transition-colors hover:bg-[var(--color-hover)]"
            style={{ color: 'var(--color-text-tertiary)' }}
          >
            <X className="h-4.5 w-4.5" />
          </button>
        </div>

        <div className="space-y-5 px-6 pt-4 pb-6">
          {/* Theme selection */}
          <div>
            <span
              className="mb-2.5 block text-[13px] font-medium uppercase tracking-wider"
              style={{ color: 'var(--color-text-tertiary)' }}
            >
              {t('settings.theme')}
            </span>
            <div className="grid grid-cols-3 gap-2.5">
              {themes.map((th) => (
                <ThemeCard
                  key={th.value}
                  themeId={th.value}
                  label={th.label}
                  selected={theme === th.value}
                  onSelect={() => setTheme(th.value)}
                />
              ))}
            </div>
          </div>

          {/* Color mode */}
          <div>
            <span
              className="mb-2.5 block text-[13px] font-medium uppercase tracking-wider"
              style={{ color: 'var(--color-text-tertiary)' }}
            >
              {t('settings.colorMode')}
            </span>
            <div className="flex gap-2.5">
              {colorModes.map((cm) => (
                <ColorModeButton
                  key={cm.value}
                  mode={cm.value}
                  label={cm.label}
                  selected={colorMode === cm.value}
                  onSelect={() => setColorMode(cm.value)}
                />
              ))}
            </div>
          </div>

          {/* Language */}
          <div>
            <span
              className="mb-2.5 block text-[13px] font-medium uppercase tracking-wider"
              style={{ color: 'var(--color-text-tertiary)' }}
            >
              {t('settings.language')}
            </span>
            <div className="flex gap-2.5">
              {LANGUAGES.map((lang) => (
                <button
                  key={lang.value}
                  type="button"
                  onClick={() => setLanguage(lang.value)}
                  className="flex flex-1 items-center justify-center gap-2 rounded-[var(--radius-md)] py-2.5 transition-all"
                  style={{
                    border:
                      currentLang === lang.value
                        ? '2px solid var(--color-accent)'
                        : '2px solid var(--color-border)',
                    backgroundColor:
                      currentLang === lang.value ? 'var(--color-accent-bg)' : 'transparent',
                  }}
                >
                  <Globe
                    className="h-4 w-4"
                    style={{
                      color:
                        currentLang === lang.value
                          ? 'var(--color-accent)'
                          : 'var(--color-text-tertiary)',
                    }}
                  />
                  <span
                    className="text-[13px] font-medium"
                    style={{
                      color:
                        currentLang === lang.value
                          ? 'var(--color-accent)'
                          : 'var(--color-text-secondary)',
                    }}
                  >
                    {lang.label}
                  </span>
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
