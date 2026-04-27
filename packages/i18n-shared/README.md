# @nodate-flow/i18n-shared

Shared `common` namespace strings reused by every web app (`apps/flow-web`, `apps/accounts-web`).

## What lives here

Atoms that show up across every app: action verbs (`save`, `cancel`, `confirm`, `delete`,
`edit`, `add`, `remove`, `submit`, `close`, `retry`), status (`loading`),
confirmation dialog title (`confirm_title`), and the picker date primitives
(`weekdays`, `monthYear`, `openPicker`, `placeholder`, `prev_month`, `next_month`).

Anything app-specific (navigation, feature copy, error variations) stays in the
app's own `locales/<lng>/common.json`.

## Merge strategy

Each app deep-merges the shared bundle with its own `common.json` at i18next init
time. **App-local keys always win on conflict.** Arrays are replaced wholesale
(not concatenated). Resolution order:

1. App-local `apps/<app>/locales/<lng>/common.json`
2. `@nodate-flow/i18n-shared/locales/<lng>/common.json`

Use the `mergeCommon(locale, appCommon)` helper from `src/index.ts` to produce
the final bundle passed to `i18next.init({ resources })`.

```ts
import { mergeCommon } from '@nodate-flow/i18n-shared';
import enCommon from '../../locales/en/common.json';

const enMerged = mergeCommon('en', enCommon);
```

## Adding a new shared key

1. Add the key to `locales/en/common.json` (source language).
2. Add the same key to `ja` and `zh` (`bun run i18n:check` enforces parity).
3. Remove any duplicate definitions from `apps/<app>/locales/<lng>/common.json`
   if the app no longer needs to override the shared value.

## Adding a new locale

Drop a new `locales/<lng>/common.json` mirroring the `en` shape and extend
`SharedLocale` plus `sharedCommon` in `src/index.ts`.
