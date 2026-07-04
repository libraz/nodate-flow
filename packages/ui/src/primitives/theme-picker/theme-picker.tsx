/**
 * ThemePicker — visual theme family selector + color mode selector.
 *
 * Renders a grid of theme preview cards (one per family) and a row of
 * color mode buttons (light / dark / system). Both are fully controlled
 * via props — no internal state or side-effects.
 *
 * Designed for reuse across all apps: pass `themes` to control which
 * families are shown, and wire `onThemeChange` / `onColorModeChange`
 * to your ThemeProvider (or Zustand store, or anything else).
 */

import { Monitor, Moon, Sun } from 'lucide-react';
import { type KeyboardEvent, type MutableRefObject, type ReactElement, useRef } from 'react';

import type { ColorMode, ThemeFamily } from '../../providers/theme-provider';
import styles from './theme-picker.module.css';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Visual preview style for a theme family card. */
export interface ThemePreviewStyle {
  /** CSS background for the preview area (gradient, solid, etc.). */
  bg: string;
  /** Accent color shown in the first bar of the mini-preview. */
  accent: string;
  /** Border-radius of the preview area (e.g. '12px', '0'). */
  radius: string;
}

/** One theme family entry. */
export interface ThemeFamilyEntry {
  /** Family id (e.g. 'glass', 'aurora', 'dotline'). */
  id: ThemeFamily;
  /** Localised display label. */
  label: string;
  /** Mini-preview styling. */
  preview: ThemePreviewStyle;
}

/** Color mode entry. */
export interface ColorModeEntry {
  /** Mode value. */
  mode: ColorMode;
  /** Localised display label. */
  label: string;
}

export interface ThemePickerProps {
  /** Available theme families to show. */
  themes: ThemeFamilyEntry[];
  /** Currently selected theme family. */
  selectedTheme: ThemeFamily;
  /** Available color modes to show. */
  colorModes?: ColorModeEntry[];
  /** Currently selected color mode. */
  selectedColorMode: ColorMode;
  /** Called when the user selects a theme family. */
  onThemeChange: (theme: ThemeFamily) => void;
  /** Called when the user selects a color mode. */
  onColorModeChange: (mode: ColorMode) => void;
  /** Label for the theme section. */
  themeLabel?: string;
  /** Label for the color mode section. */
  colorModeLabel?: string;
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

/** Default preview styles for the built-in theme families. */
export const DEFAULT_THEME_PREVIEWS: Record<ThemeFamily, ThemePreviewStyle> = {
  glass: {
    bg: 'linear-gradient(135deg, oklch(96% 0.01 260), oklch(94% 0.008 280))',
    accent: 'oklch(52% 0.12 260)',
    radius: '6px',
  },
  aurora: {
    bg: 'linear-gradient(135deg, oklch(92% 0.04 150), oklch(90% 0.04 175))',
    accent: 'oklch(72% 0.13 175)',
    radius: '10px',
  },
  dotline: {
    bg: 'oklch(94% 0.002 0)',
    accent: 'oklch(60% 0.21 25)',
    radius: '0',
  },
};

const DEFAULT_COLOR_MODES: ColorModeEntry[] = [
  { mode: 'light', label: 'Light' },
  { mode: 'dark', label: 'Dark' },
  { mode: 'system', label: 'System' },
];

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

const ModeIcon = ({ mode }: { mode: ColorMode }): ReactElement => {
  const size = 18;
  switch (mode) {
    case 'light':
      return <Sun size={size} aria-hidden className={styles.modeIcon} />;
    case 'dark':
      return <Moon size={size} aria-hidden className={styles.modeIcon} />;
    case 'system':
      return <Monitor size={size} aria-hidden className={styles.modeIcon} />;
  }
};

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

/** ThemePicker renders a theme family grid + color mode row. */
export default function ThemePicker({
  themes,
  selectedTheme,
  colorModes = DEFAULT_COLOR_MODES,
  selectedColorMode,
  onThemeChange,
  onColorModeChange,
  themeLabel,
  colorModeLabel,
}: ThemePickerProps): ReactElement {
  const themeRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const colorModeRefs = useRef<Array<HTMLButtonElement | null>>([]);

  const handleRadioKeyDown = <T,>(
    event: KeyboardEvent<HTMLButtonElement>,
    entries: readonly T[],
    currentIndex: number,
    refs: MutableRefObject<Array<HTMLButtonElement | null>>,
    select: (entry: T) => void,
  ): void => {
    let nextIndex = currentIndex;
    const rtl =
      event.currentTarget.closest('[dir="rtl"]') !== null || document.documentElement.dir === 'rtl';
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        nextIndex =
          event.key === 'ArrowRight' && rtl
            ? (currentIndex - 1 + entries.length) % entries.length
            : (currentIndex + 1) % entries.length;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        nextIndex =
          event.key === 'ArrowLeft' && rtl
            ? (currentIndex + 1) % entries.length
            : (currentIndex - 1 + entries.length) % entries.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = entries.length - 1;
        break;
      default:
        return;
    }

    event.preventDefault();
    const next = entries[nextIndex];
    if (next === undefined) return;
    select(next);
    refs.current[nextIndex]?.focus();
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}>
      {/* Theme families */}
      <div className={styles.section}>
        {themeLabel ? <span className={styles.label}>{themeLabel}</span> : null}
        <div className={styles.themeGrid} role="radiogroup" aria-label={themeLabel ?? 'Theme'}>
          {themes.map((entry) => {
            const index = themes.indexOf(entry);
            const selected = entry.id === selectedTheme;
            return (
              <button
                key={entry.id}
                ref={(node) => {
                  themeRefs.current[index] = node;
                }}
                type="button"
                role="radio"
                aria-checked={selected}
                tabIndex={selected ? 0 : -1}
                data-selected={selected}
                className={styles.themeCard}
                onClick={() => onThemeChange(entry.id)}
                onKeyDown={(event) =>
                  handleRadioKeyDown(event, themes, index, themeRefs, (next) =>
                    onThemeChange(next.id),
                  )
                }
              >
                <div
                  className={styles.preview}
                  style={{ background: entry.preview.bg, borderRadius: entry.preview.radius }}
                >
                  <div className={styles.previewBars}>
                    {[1, 2, 3].map((i) => (
                      <div
                        key={i}
                        className={styles.previewBar}
                        style={{
                          flex: i === 2 ? 2 : 1,
                          backgroundColor:
                            i === 1 ? entry.preview.accent : 'var(--nf-color-border)',
                          borderRadius: entry.preview.radius === '0' ? '0' : '3px',
                        }}
                      />
                    ))}
                  </div>
                </div>
                <span className={styles.themeName}>{entry.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Color modes */}
      <div className={styles.section}>
        {colorModeLabel ? <span className={styles.label}>{colorModeLabel}</span> : null}
        <div
          className={styles.modeGrid}
          role="radiogroup"
          aria-label={colorModeLabel ?? 'Color mode'}
        >
          {colorModes.map((entry) => {
            const index = colorModes.indexOf(entry);
            const selected = entry.mode === selectedColorMode;
            return (
              <button
                key={entry.mode}
                ref={(node) => {
                  colorModeRefs.current[index] = node;
                }}
                type="button"
                role="radio"
                aria-checked={selected}
                tabIndex={selected ? 0 : -1}
                data-selected={selected}
                className={styles.modeButton}
                onClick={() => onColorModeChange(entry.mode)}
                onKeyDown={(event) =>
                  handleRadioKeyDown(event, colorModes, index, colorModeRefs, (next) =>
                    onColorModeChange(next.mode),
                  )
                }
              >
                <ModeIcon mode={entry.mode} />
                <span className={styles.modeName}>{entry.label}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
