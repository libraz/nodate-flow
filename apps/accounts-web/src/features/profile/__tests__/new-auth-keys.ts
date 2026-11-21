// biome-ignore-all lint/style/useNamingConvention: keys mirror the snake_case locale JSON
/**
 * English copy for the keys these tests exercise. Merged over the real
 * `auth.json` so the tests also hold before the locale files carry them.
 */
import enAuth from '../../../../locales/en/auth.json';

type Json = Record<string, unknown>;

export const newKeys = {
  profile: {
    color_mode: 'Color mode',
    theme_glass: 'Glass',
    theme_modern: 'Modern',
    theme_washi: 'Washi',
    theme_classic: 'Classic',
    theme_nothing: 'Nothing',
    color_mode_light: 'Light',
    color_mode_dark: 'Dark',
    color_mode_system: 'System',
    avatar: {
      upload_aria: 'Upload profile picture',
      replace_aria: 'Replace profile picture',
      upload_error: 'Could not upload the profile picture. Please try again.',
      remove_error: 'Could not remove the profile picture. Please try again.',
      remove_confirm: 'Remove your profile picture?',
    },
  },
  security: {
    session_revoke_all: 'Sign out of all other devices',
    session_revoke_all_confirm:
      'Sign out of all other devices? Other browsers and apps will stop working immediately.',
    session_revoked_all: 'Revoked {count} other session(s)',
    session_revoke_all_failed: 'Could not revoke the other sessions. Please try again.',
    totp: {
      recovery: {
        remaining:
          '{count, plural, one {# recovery code remaining} other {# recovery codes remaining}}',
      },
    },
  },
} as const;

function merge(base: Json, extra: Json): Json {
  const out: Json = { ...base };
  for (const [k, v] of Object.entries(extra)) {
    const cur = out[k];
    out[k] =
      v && typeof v === 'object' && cur && typeof cur === 'object'
        ? merge(cur as Json, v as Json)
        : v;
  }
  return out;
}

export const authResources = merge(enAuth as Json, newKeys as Json) as typeof enAuth & {
  profile: { color_mode: string };
};
