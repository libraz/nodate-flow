import enCommon from '../locales/en/common.json';
import jaCommon from '../locales/ja/common.json';
import zhCommon from '../locales/zh/common.json';

/** Locale code for shared common strings. */
export type SharedLocale = 'en' | 'ja' | 'zh';

/**
 * Shared `common` namespace bundles, keyed by locale.
 * Apps deep-merge these with their own `common.json` so app-local keys win on conflict.
 */
export const sharedCommon: Record<SharedLocale, unknown> = {
  en: enCommon,
  ja: jaCommon,
  zh: zhCommon,
};

/**
 * Deep-merge two plain objects, returning a new object. Values from `override`
 * take precedence. Arrays are replaced wholesale (not concatenated). Primitives
 * and arrays in `override` overwrite the corresponding key in `base`.
 */
export function deepMerge<T extends Record<string, unknown>>(base: T, override: Partial<T>): T {
  const out: Record<string, unknown> = { ...base };
  for (const key of Object.keys(override)) {
    const a = (base as Record<string, unknown>)[key];
    const b = (override as Record<string, unknown>)[key];
    if (isPlainObject(a) && isPlainObject(b)) {
      out[key] = deepMerge(a, b);
    } else if (b !== undefined) {
      out[key] = b;
    }
  }
  return out as T;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return (
    typeof value === 'object' &&
    value !== null &&
    !Array.isArray(value) &&
    Object.getPrototypeOf(value) === Object.prototype
  );
}

/**
 * Build the merged `common` namespace bundle for the given locale by deep-merging
 * the shared bundle with the app-local one (app-local wins).
 */
export function mergeCommon(
  locale: SharedLocale,
  appLocal: Record<string, unknown>,
): Record<string, unknown> {
  const shared = sharedCommon[locale] as Record<string, unknown>;
  return deepMerge(shared, appLocal);
}
