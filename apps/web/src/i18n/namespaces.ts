/**
 * List of all i18next namespaces used by the web app. `common` is eager;
 * every other namespace is lazy-loaded per-route.
 */
export const namespaces = [
  'common',
  'auth',
  'workspaces',
  'projects',
  'tasks',
  'comments',
  'timeline',
  'inbox',
  'settings',
  'ai',
  'ai-suggestions',
  'mcp',
  'errors',
] as const;

/** Union of all known i18n namespace names. */
export type Namespace = (typeof namespaces)[number];

/** The eagerly loaded default namespace. */
export const defaultNamespace: Namespace = 'common';
