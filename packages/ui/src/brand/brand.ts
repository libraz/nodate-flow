/**
 * Deployment branding rules: app name and logo URL resolved from build-time
 * env. Pure functions only (no imports, no Node APIs) so the Vite plugin and
 * the app bundle can share them.
 */

export const DEFAULT_APP_NAME = 'nodate-flow';

const MAX_NAME_CODE_POINTS = 64;
const MAX_DATA_URI_LENGTH = 32_768;
const DATA_URI_PREFIX = /^data:image\/(?:png|svg\+xml|webp|jpeg|gif)[;,]/;

// C0, DEL, C1, U+2028/2029, bidi controls, and ':' (otpauth label separator).
// biome-ignore lint/suspicious/noControlCharactersInRegex: the rule is exactly these control characters
const FORBIDDEN_NAME_CHARS = /[\u0000-\u001F\u007F-\u009F\u2028\u2029\u202A-\u202E\u2066-\u2069:]/;

// Characters that cannot appear in a file name.
// biome-ignore lint/suspicious/noControlCharactersInRegex: control characters are the point
const FILE_UNSAFE_CHARS = /[/\\:*?"<>|\u0000-\u001F\u007F]/g;

export interface Brand {
  appName: string;
  logoUrl: string | undefined;
}

function resolveName(raw: unknown): string {
  const name = typeof raw === 'string' ? raw.trim() : '';
  if (name === '') {
    return DEFAULT_APP_NAME;
  }
  if ([...name].length > MAX_NAME_CODE_POINTS) {
    throw new Error(`VITE_APP_NAME must be at most ${MAX_NAME_CODE_POINTS} characters`);
  }
  if (FORBIDDEN_NAME_CHARS.test(name)) {
    throw new Error('VITE_APP_NAME must not contain control characters, bidi controls, or ":"');
  }
  return name;
}

function resolveLogo(raw: unknown): string | undefined {
  const logo = typeof raw === 'string' ? raw.trim() : '';
  if (logo === '') {
    return undefined;
  }
  if (logo.slice(0, 5).toLowerCase() === 'data:') {
    if (!DATA_URI_PREFIX.test(logo.toLowerCase())) {
      throw new Error(
        'VITE_APP_LOGO_URL data URI must be image/png, image/svg+xml, image/webp, image/jpeg or image/gif',
      );
    }
    if (logo.length > MAX_DATA_URI_LENGTH) {
      throw new Error(
        `VITE_APP_LOGO_URL data URI must be at most ${MAX_DATA_URI_LENGTH} characters`,
      );
    }
    return logo;
  }
  let protocol: string;
  try {
    protocol = new URL(logo).protocol;
  } catch {
    throw new Error('VITE_APP_LOGO_URL must be an absolute https URL or an image data URI');
  }
  if (protocol !== 'https:') {
    throw new Error('VITE_APP_LOGO_URL must use https or be an image data URI');
  }
  return logo;
}

/** Validates `VITE_APP_NAME` / `VITE_APP_LOGO_URL`; throws a named error on a bad value. */
export function resolveBrand(env: Record<string, unknown>): Brand {
  return {
    appName: resolveName(env.VITE_APP_NAME),
    logoUrl: resolveLogo(env.VITE_APP_LOGO_URL),
  };
}

/** File-name-safe form of the app name; falls back to the default when nothing remains. */
export function brandFileStem(appName: string): string {
  const stem = appName
    .replace(FILE_UNSAFE_CHARS, '-')
    .replace(/\s+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '');
  return stem === '' ? DEFAULT_APP_NAME : stem;
}
