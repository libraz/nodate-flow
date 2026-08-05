// Conventional commits configuration; aligns with docs/conventions/code-style.md.
//
// The shape of a message -- type prefix, no trailing period, blank line then
// bullet body -- is enforced for every repository on this machine. What only
// this repository can know is which scopes name something real in a monorepo
// that ships several apps, so that is what this file is mostly for.
//
// Scope policy:
// - scope is REQUIRED for the types that change behaviour: feat, fix,
//   refactor, perf and test. A `ci:` or `docs:` change that spans the
//   repository does not need one.
// - scopes come from the allowlist below, which names every app and package
//   plus the cross-cutting concerns. `api`, `web` and `auth` are deliberately
//   absent: this repository has more than one of each, so those scopes say
//   nothing about where a change landed.
const allowedScopes = [
  // apps
  'flow-api',
  'auth-api',
  'flow-worker',
  'presence-discord',
  'flow-web',
  'accounts-web',
  'cli',
  // packages
  'go-shared',
  'sdk',
  'ui',
  'holidays',
  'i18n-shared',
  // features that span apps
  'ai',
  'calendar',
  'tasks',
  'mcp',
  // cross-cutting
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

// Types whose whole point is that they change how something behaves; for
// those, "where" belongs in the message rather than being optional.
const scopedTypes = ['feat', 'fix', 'refactor', 'perf', 'test'];

export default {
  extends: ['@commitlint/config-conventional'],
  plugins: [
    {
      rules: {
        'scope-required-for-behaviour': ({ type, scope }) => {
          if (type && scopedTypes.includes(type) && !scope) {
            return [false, `scope is required for ${scopedTypes.join(', ')} commits`];
          }
          return [true];
        },
      },
    },
  ],
  rules: {
    'subject-max-length': [2, 'always', 72],
    // Subject casing is left to the inherited rule, which rejects a subject
    // that is entirely title-cased or upper-cased. Demanding lower case
    // outright would mean writing proper nouns wrong.
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
    'scope-required-for-behaviour': [2, 'always'],
    // Bodies wrap at whatever keeps the sentence readable; a hard column here
    // would reject explanations that are worth their length.
    'body-max-line-length': [0],
  },
};
