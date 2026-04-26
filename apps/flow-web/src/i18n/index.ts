import i18n from 'i18next';
import ICU from 'i18next-icu';
import { initReactI18next } from 'react-i18next';

import enAiSuggestions from '../../locales/en/ai-suggestions.json';
import enAi from '../../locales/en/ai.json';
import enArchive from '../../locales/en/archive.json';
import enAuth from '../../locales/en/auth.json';
import enCalendarEvents from '../../locales/en/calendar-events.json';
import enCommon from '../../locales/en/common.json';
import enConstraints from '../../locales/en/constraints.json';
import enDashboard from '../../locales/en/dashboard.json';
import enErrors from '../../locales/en/errors.json';
import enInbox from '../../locales/en/inbox.json';
import enLabels from '../../locales/en/labels.json';
import enLinkedEvents from '../../locales/en/linkedEvents.json';
import enNotifications from '../../locales/en/notifications.json';
import enPages from '../../locales/en/pages.json';
import enReactions from '../../locales/en/reactions.json';
import enRelations from '../../locales/en/relations.json';
import enSettings from '../../locales/en/settings.json';
import enTimeline from '../../locales/en/timeline.json';
import jaAiSuggestions from '../../locales/ja/ai-suggestions.json';
import jaAi from '../../locales/ja/ai.json';
import jaArchive from '../../locales/ja/archive.json';
import jaAuth from '../../locales/ja/auth.json';
import jaCalendarEvents from '../../locales/ja/calendar-events.json';
import jaCommon from '../../locales/ja/common.json';
import jaConstraints from '../../locales/ja/constraints.json';
import jaDashboard from '../../locales/ja/dashboard.json';
import jaErrors from '../../locales/ja/errors.json';
import jaInbox from '../../locales/ja/inbox.json';
import jaLabels from '../../locales/ja/labels.json';
import jaLinkedEvents from '../../locales/ja/linkedEvents.json';
import jaNotifications from '../../locales/ja/notifications.json';
import jaPages from '../../locales/ja/pages.json';
import jaReactions from '../../locales/ja/reactions.json';
import jaRelations from '../../locales/ja/relations.json';
import jaSettings from '../../locales/ja/settings.json';
import jaTimeline from '../../locales/ja/timeline.json';
import zhAiSuggestions from '../../locales/zh/ai-suggestions.json';
import zhAi from '../../locales/zh/ai.json';
import zhArchive from '../../locales/zh/archive.json';
import zhAuth from '../../locales/zh/auth.json';
import zhCalendarEvents from '../../locales/zh/calendar-events.json';
import zhCommon from '../../locales/zh/common.json';
import zhConstraints from '../../locales/zh/constraints.json';
import zhDashboard from '../../locales/zh/dashboard.json';
import zhErrors from '../../locales/zh/errors.json';
import zhInbox from '../../locales/zh/inbox.json';
import zhLabels from '../../locales/zh/labels.json';
import zhLinkedEvents from '../../locales/zh/linkedEvents.json';
import zhNotifications from '../../locales/zh/notifications.json';
import zhPages from '../../locales/zh/pages.json';
import zhReactions from '../../locales/zh/reactions.json';
import zhRelations from '../../locales/zh/relations.json';
import zhSettings from '../../locales/zh/settings.json';
import zhTimeline from '../../locales/zh/timeline.json';
import { defaultNamespace } from './namespaces';

/** Supported UI languages. */
export const supportedLanguages = ['en', 'ja', 'zh'] as const;

/** Union of supported UI language codes. */
export type SupportedLanguage = (typeof supportedLanguages)[number];

const languageStorageKey = 'nf.lang';

function detectInitialLanguage(): SupportedLanguage {
  try {
    const stored = localStorage.getItem(languageStorageKey);
    if (stored && (supportedLanguages as readonly string[]).includes(stored)) {
      return stored as SupportedLanguage;
    }
  } catch {
    // ignore
  }
  const nav = typeof navigator !== 'undefined' ? navigator.language : 'en';
  const lower = nav.toLowerCase();
  if (lower.startsWith('ja')) return 'ja';
  if (lower.startsWith('zh')) return 'zh';
  return 'en';
}

/** Initialize the singleton i18next instance. Safe to call multiple times. */
export function initI18n(): typeof i18n {
  if (i18n.isInitialized) return i18n;
  void i18n
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: detectInitialLanguage(),
      fallbackLng: 'en',
      supportedLngs: supportedLanguages as unknown as string[],
      defaultNS: defaultNamespace,
      ns: [
        defaultNamespace,
        'settings',
        'inbox',
        'timeline',
        'calendar-events',
        'ai',
        'ai-suggestions',
        'constraints',
        'errors',
        'notifications',
        'relations',
        'dashboard',
        'pages',
        'labels',
        'reactions',
        'archive',
        'linkedEvents',
      ],
      resources: {
        en: {
          archive: enArchive,
          auth: enAuth,
          common: enCommon,
          settings: enSettings,
          inbox: enInbox,
          timeline: enTimeline,
          'calendar-events': enCalendarEvents,
          ai: enAi,
          'ai-suggestions': enAiSuggestions,
          constraints: enConstraints,
          errors: enErrors,
          notifications: enNotifications,
          relations: enRelations,
          dashboard: enDashboard,
          pages: enPages,
          labels: enLabels,
          linkedEvents: enLinkedEvents,
          reactions: enReactions,
        },
        ja: {
          archive: jaArchive,
          auth: jaAuth,
          common: jaCommon,
          settings: jaSettings,
          inbox: jaInbox,
          timeline: jaTimeline,
          'calendar-events': jaCalendarEvents,
          ai: jaAi,
          'ai-suggestions': jaAiSuggestions,
          constraints: jaConstraints,
          errors: jaErrors,
          notifications: jaNotifications,
          relations: jaRelations,
          dashboard: jaDashboard,
          pages: jaPages,
          labels: jaLabels,
          linkedEvents: jaLinkedEvents,
          reactions: jaReactions,
        },
        zh: {
          archive: zhArchive,
          auth: zhAuth,
          common: zhCommon,
          settings: zhSettings,
          inbox: zhInbox,
          timeline: zhTimeline,
          'calendar-events': zhCalendarEvents,
          ai: zhAi,
          'ai-suggestions': zhAiSuggestions,
          constraints: zhConstraints,
          errors: zhErrors,
          notifications: zhNotifications,
          relations: zhRelations,
          dashboard: zhDashboard,
          pages: zhPages,
          labels: zhLabels,
          linkedEvents: zhLinkedEvents,
          reactions: zhReactions,
        },
      },
      interpolation: { escapeValue: false },
      react: { useSuspense: true },
    });
  return i18n;
}

/** Persist and apply a new UI language. */
export function setLanguage(lang: SupportedLanguage): void {
  try {
    localStorage.setItem(languageStorageKey, lang);
  } catch {
    // ignore
  }
  void i18n.changeLanguage(lang);
}

export { i18n };
