/**
 * List of all i18next namespaces used by the web app. `common` is eager;
 * every other namespace is lazy-loaded per-route.
 */
export const namespaces = [
  'common',
  'timeline',
  'activity',
  'calendar-events',
  'inbox',
  'settings',
  'ai',
  'ai-suggestions',
  'aiPriority',
  'aiMetrics',
  'errors',
  'notifications',
  'relations',
  'dashboard',
  'pages',
  'labels',
  'reactions',
  'archive',
  'linkedEvents',
  'aiAgents',
  'sharing',
  'signal-kinds',
  'system-info',
] as const;

/** Union of all known i18n namespace names. */
export type Namespace = (typeof namespaces)[number];

/** The eagerly loaded default namespace. */
export const defaultNamespace: Namespace = 'common';
