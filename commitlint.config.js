// Conventional commits configuration; aligns with docs/conventions/code-style.md.
//
// Scope policy:
// - scope is REQUIRED for feat/fix/refactor/perf/test commits.
// - scopes must come from the allowlist below; ambiguous scopes such as
//   `api` or `web` are rejected to keep history navigable in a monorepo
//   that ships multiple apps.
const allowedScopes = [
  'flow-api',
  'auth-api',
  'flow-web',
  'accounts-web',
  'cli',
  'sdk',
  'ui',
  'go-shared',
  'sql',
  'errors',
  'i18n',
  'docs',
  'infra',
  'ci',
  'deps',
  'make',
  'test',
];

export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'subject-max-length': [2, 'always', 72],
    'subject-case': [2, 'always', 'lower-case'],
    'type-enum': [
      2,
      'always',
      [
        'feat',
        'fix',
        'docs',
        'style',
        'refactor',
        'test',
        'chore',
        'perf',
        'build',
        'ci',
        'revert',
      ],
    ],
    'scope-enum': [2, 'always', allowedScopes],
    'scope-empty': [2, 'never'],
  },
};
