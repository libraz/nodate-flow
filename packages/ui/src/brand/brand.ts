/**
 * Deployment branding rules: app name, logo and icon images resolved from build-time
 * env. Pure functions only (no imports, no Node APIs) so the Vite plugin and
 * the app bundle can share them.
 */

export const DEFAULT_APP_NAME = 'nodate-flow';

const MAX_NAME_CODE_POINTS = 64;
const MAX_DATA_URI_LENGTH = 32_768;
const DATA_URI_PREFIX = /^data:image\/(?:png|svg\+xml|webp|jpeg|gif)[;,]/i;

// C0, DEL, C1, U+2028/2029, bidi controls, and ':' (otpauth label separator).
// biome-ignore lint/suspicious/noControlCharactersInRegex: the rule is exactly these control characters
const FORBIDDEN_NAME_CHARS = /[\u0000-\u001F\u007F-\u009F\u2028\u2029\u202A-\u202E\u2066-\u2069:]/;

// Characters that cannot appear in a file name.
// biome-ignore lint/suspicious/noControlCharactersInRegex: control characters are the point
const FILE_UNSAFE_CHARS = /[/\\:*?"<>|\u0000-\u001F\u007F]/g;

export interface BrandAsset {
  src: string;
  darkSrc: string | undefined;
}

export interface Brand {
  appName: string;
  logo: BrandAsset | undefined;
  icon: BrandAsset | undefined;
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

/** Leading/trailing ASCII whitespace only; `.trim()` also strips Unicode spaces. */
const ASCII_WS_EDGES = /^[ \t\n\r\f\v]+|[ \t\n\r\f\v]+$/g;
const PRINTABLE_ASCII = /^[\x21-\x7E]+$/;

function imageError(variable: string): Error {
  return new Error(
    `${variable} must be an https URL or a data:image/(png|svg+xml|webp|jpeg|gif) URI`,
  );
}

/** True when `value` starts with `data:`, ignoring case. */
export function isDataUri(value: string): boolean {
  return value.slice(0, 5).toLowerCase() === 'data:';
}

function resolveImage(variable: string, raw: unknown): string | undefined {
  const value = typeof raw === 'string' ? raw.replace(ASCII_WS_EDGES, '') : '';
  if (value === '') {
    return undefined;
  }
  if (!PRINTABLE_ASCII.test(value)) {
    throw new Error(`${variable} must contain only printable ASCII characters`);
  }
  if (isDataUri(value)) {
    if (!DATA_URI_PREFIX.test(value)) {
      throw imageError(variable);
    }
    if (value.length > MAX_DATA_URI_LENGTH) {
      throw new Error(`${variable} data URI must be at most ${MAX_DATA_URI_LENGTH} characters`);
    }
    return value;
  }
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw imageError(variable);
  }
  if (url.protocol.toLowerCase() !== 'https:' || url.host === '') {
    throw imageError(variable);
  }
  return value;
}

function resolveAsset(baseVar: string, darkVar: string, env: Record<string, unknown>) {
  const src = resolveImage(baseVar, env[baseVar]);
  const darkSrc = resolveImage(darkVar, env[darkVar]);
  if (src === undefined) {
    if (darkSrc !== undefined) {
      throw new Error(`${darkVar} requires ${baseVar}`);
    }
    return undefined;
  }
  return { src, darkSrc };
}

/** Validates the `VITE_APP_*` brand variables; throws a named error on a bad value. */
export function resolveBrand(env: Record<string, unknown>): Brand {
  return {
    appName: resolveName(env.VITE_APP_NAME),
    logo: resolveAsset('VITE_APP_LOGO_URL', 'VITE_APP_LOGO_DARK_URL', env),
    icon: resolveAsset('VITE_APP_ICON_URL', 'VITE_APP_ICON_DARK_URL', env),
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
