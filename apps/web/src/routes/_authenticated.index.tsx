/**
 * Authenticated home (/). Moved from routes/index.tsx as part of F3 so
 * the route can sit under the _authenticated layout guard.
 */

import { createFileRoute } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { type SupportedLanguage, setLanguage, supportedLanguages } from '../i18n';
import {
  type ThemePreference,
  type concreteThemes,
  themePreferences,
  useTheme,
} from '../providers/theme-provider';

function LanguageSwitcher(): ReactElement {
  const { t, i18n } = useTranslation('common');
  const current = (i18n.resolvedLanguage ?? 'en') as SupportedLanguage;
  return (
    <fieldset
      style={{
        border: '1px solid var(--color-hairline)',
        borderRadius: '999px',
        padding: '0.125rem',
        display: 'inline-flex',
        gap: '0.125rem',
      }}
    >
      <legend
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: '0.625rem',
          letterSpacing: '0.08em',
          color: 'var(--color-muted)',
          paddingInline: '0.5rem',
        }}
      >
        {t('nav.language')}
      </legend>
      {supportedLanguages.map((lng) => {
        const active = lng === current;
        const label = lng === 'en' ? t('lang.en') : t('lang.ja');
        return (
          <button
            key={lng}
            type="button"
            onClick={() => {
              setLanguage(lng);
            }}
            style={{
              background: active ? 'var(--color-fg)' : 'transparent',
              color: active ? 'var(--color-bg)' : 'var(--color-fg)',
              border: 'none',
              borderRadius: '999px',
              paddingBlock: '0.375rem',
              paddingInline: '0.75rem',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              cursor: 'pointer',
            }}
          >
            {label}
          </button>
        );
      })}
    </fieldset>
  );
}

function ThemeSwitcher(): ReactElement {
  const { t } = useTranslation('common');
  const { preference, setPreference } = useTheme();
  return (
    <fieldset
      style={{
        border: '1px solid var(--color-hairline)',
        borderRadius: '999px',
        padding: '0.125rem',
        display: 'inline-flex',
        gap: '0.125rem',
        flexWrap: 'wrap',
      }}
    >
      <legend
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: '0.625rem',
          letterSpacing: '0.08em',
          color: 'var(--color-muted)',
          paddingInline: '0.5rem',
        }}
      >
        {t('nav.theme')}
      </legend>
      {themePreferences.map((pref: ThemePreference) => {
        const active = pref === preference;
        return (
          <button
            key={pref}
            type="button"
            onClick={() => {
              setPreference(pref);
            }}
            style={{
              background: active ? 'var(--color-fg)' : 'transparent',
              color: active ? 'var(--color-bg)' : 'var(--color-fg)',
              border: 'none',
              borderRadius: '999px',
              paddingBlock: '0.375rem',
              paddingInline: '0.75rem',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              cursor: 'pointer',
            }}
          >
            {pref === 'system' ? t('theme.system') : t(themeLabelKey(pref))}
          </button>
        );
      })}
    </fieldset>
  );
}

function themeLabelKey(
  name: (typeof concreteThemes)[number],
): 'theme.aurora-light' | 'theme.aurora-dark' | 'theme.dotline-light' | 'theme.dotline-dark' {
  switch (name) {
    case 'aurora-light':
      return 'theme.aurora-light';
    case 'aurora-dark':
      return 'theme.aurora-dark';
    case 'dotline-light':
      return 'theme.dotline-light';
    case 'dotline-dark':
      return 'theme.dotline-dark';
  }
}

function LandingPage(): ReactElement {
  const { t } = useTranslation('common');
  return (
    <section
      style={{
        minBlockSize: '100%',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
        paddingBlock: '3rem',
        paddingInline: 'clamp(1.5rem, 6vw, 5rem)',
      }}
    >
      <header
        style={{
          fontFamily: 'var(--font-mono)',
          fontSize: '0.6875rem',
          letterSpacing: '0.18em',
          color: 'var(--color-muted)',
        }}
      >
        {t('landing.tagline')}
      </header>

      <section style={{ display: 'flex', flexDirection: 'column', gap: '2rem' }}>
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontOpticalSizing: 'auto',
            fontWeight: 400,
            fontSize: 'clamp(3rem, 10vw, 7.5rem)',
            lineHeight: 0.95,
            letterSpacing: '-0.02em',
            margin: 0,
          }}
        >
          {t('landing.wordmark')}
        </h1>
        <p
          style={{
            fontFamily: 'var(--font-display)',
            fontStyle: 'italic',
            fontSize: 'clamp(1.25rem, 2.5vw, 1.75rem)',
            color: 'var(--color-muted)',
            maxInlineSize: '36ch',
            margin: 0,
          }}
        >
          {t('landing.tagline')}
        </p>
        <p
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'clamp(1rem, 1.75vw, 1.25rem)',
            margin: 0,
          }}
        >
          {t('landing.hello')}
        </p>
      </section>

      <footer
        style={{
          display: 'flex',
          gap: '1rem',
          flexWrap: 'wrap',
          alignItems: 'center',
        }}
      >
        <ThemeSwitcher />
        <LanguageSwitcher />
      </footer>
    </section>
  );
}

export const Route = createFileRoute('/_authenticated/')({
  component: LandingPage,
});
