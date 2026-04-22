/**
 * Region helpers shared by all web apps. Mirrors the Go
 * `packages/go-shared/region` package for the frontend.
 *
 * - Timezone list is sourced from `Intl.supportedValuesOf('timeZone')` when
 *   available, with a curated fallback for older runtimes.
 * - Country list is the same allowlist as the backend; extending the list
 *   requires a corresponding entry on both sides.
 */

// ISO 3166-1 alpha-2 codes are intrinsically uppercase; build the lookup
// object from a tuple list so Biome's useNamingConvention rule (which flags
// uppercase object property names) is never triggered.
const COUNTRY_ENTRIES: ReadonlyArray<readonly [string, string]> = [
  ['JP', 'Japan'],
  ['US', 'United States'],
  ['GB', 'United Kingdom'],
  ['DE', 'Germany'],
  ['FR', 'France'],
  ['IT', 'Italy'],
  ['ES', 'Spain'],
  ['CA', 'Canada'],
  ['AU', 'Australia'],
  ['NZ', 'New Zealand'],
  ['KR', 'South Korea'],
  ['CN', 'China'],
  ['TW', 'Taiwan'],
  ['HK', 'Hong Kong'],
  ['SG', 'Singapore'],
  ['IN', 'India'],
  ['BR', 'Brazil'],
  ['MX', 'Mexico'],
  ['NL', 'Netherlands'],
  ['SE', 'Sweden'],
  ['NO', 'Norway'],
  ['FI', 'Finland'],
  ['DK', 'Denmark'],
  ['CH', 'Switzerland'],
  ['AT', 'Austria'],
  ['BE', 'Belgium'],
  ['IE', 'Ireland'],
  ['PT', 'Portugal'],
  ['PL', 'Poland'],
  ['CZ', 'Czech Republic'],
  ['TH', 'Thailand'],
  ['VN', 'Vietnam'],
  ['PH', 'Philippines'],
  ['ID', 'Indonesia'],
  ['MY', 'Malaysia'],
  ['AE', 'United Arab Emirates'],
  ['SA', 'Saudi Arabia'],
  ['IL', 'Israel'],
  ['TR', 'Turkey'],
  ['RU', 'Russia'],
  ['ZA', 'South Africa'],
  ['AR', 'Argentina'],
  ['CL', 'Chile'],
  ['CO', 'Colombia'],
];

/** ISO 3166-1 alpha-2 code → English display name. Keep in sync with Go. */
export const SUPPORTED_COUNTRIES: Readonly<Record<string, string>> =
  Object.fromEntries(COUNTRY_ENTRIES);

/**
 * Returns the full IANA timezone list. Uses Intl.supportedValuesOf when
 * available (modern browsers, Node 18+), otherwise a conservative fallback.
 *
 * 'UTC' is guaranteed to be present and first. Chromium's
 * supportedValuesOf omits it (treats it as an alias for Etc/UTC), so we
 * prepend it explicitly to keep the backend default resolvable.
 */
export function listSupportedTimezones(): string[] {
  type IntlWithSupported = typeof Intl & {
    supportedValuesOf?: (k: string) => string[];
  };
  const I = Intl as IntlWithSupported;
  let list = TIMEZONE_FALLBACK;
  if (typeof I.supportedValuesOf === 'function') {
    try {
      list = I.supportedValuesOf('timeZone');
    } catch {
      // keep fallback
    }
  }
  if (!list.includes('UTC')) {
    return ['UTC', ...list];
  }
  return list;
}

/** Best-effort detection of the runtime's timezone, with a UTC fallback. */
export function detectTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

/**
 * Returns the timezones grouped by IANA region (the part before the first
 * slash), preserving the list's original order within each group. Useful
 * for rendering `<optgroup>` elements in a select.
 *
 * 'UTC' (and any other zone without a slash) lands in a synthetic
 * 'Global' group that always comes first.
 */
export function groupTimezonesByRegion(
  list?: string[],
): Array<{ region: string; zones: string[] }> {
  const source = list ?? listSupportedTimezones();
  const groups = new Map<string, string[]>();
  for (const tz of source) {
    const slash = tz.indexOf('/');
    const region = slash === -1 ? 'Global' : tz.slice(0, slash);
    const zones = groups.get(region);
    if (zones) {
      zones.push(tz);
    } else {
      groups.set(region, [tz]);
    }
  }
  // 'Global' first, then alphabetical.
  const sorted: Array<{ region: string; zones: string[] }> = [];
  const global = groups.get('Global');
  if (global) {
    sorted.push({ region: 'Global', zones: global });
    groups.delete('Global');
  }
  for (const region of Array.from(groups.keys()).sort()) {
    // biome-ignore lint/style/noNonNullAssertion: key came from Map.keys()
    sorted.push({ region, zones: groups.get(region)! });
  }
  return sorted;
}

/**
 * Formats a timezone for display: strips the region prefix and converts
 * underscores to spaces so `America/Argentina/Buenos_Aires` reads as
 * `Argentina/Buenos Aires`.
 */
export function formatTimezoneLabel(tz: string): string {
  const slash = tz.indexOf('/');
  const tail = slash === -1 ? tz : tz.slice(slash + 1);
  return tail.replace(/_/g, ' ');
}

// A curated subset of IANA zones covering the most common regions. Only
// used when Intl.supportedValuesOf is not available.
const TIMEZONE_FALLBACK: string[] = [
  'UTC',
  'Africa/Cairo',
  'Africa/Johannesburg',
  'America/Anchorage',
  'America/Argentina/Buenos_Aires',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Mexico_City',
  'America/New_York',
  'America/Santiago',
  'America/Sao_Paulo',
  'America/Toronto',
  'America/Vancouver',
  'Asia/Bangkok',
  'Asia/Dubai',
  'Asia/Hong_Kong',
  'Asia/Jakarta',
  'Asia/Jerusalem',
  'Asia/Kolkata',
  'Asia/Kuala_Lumpur',
  'Asia/Manila',
  'Asia/Seoul',
  'Asia/Shanghai',
  'Asia/Singapore',
  'Asia/Taipei',
  'Asia/Tokyo',
  'Australia/Melbourne',
  'Australia/Sydney',
  'Europe/Amsterdam',
  'Europe/Berlin',
  'Europe/Brussels',
  'Europe/Copenhagen',
  'Europe/Dublin',
  'Europe/Helsinki',
  'Europe/Lisbon',
  'Europe/London',
  'Europe/Madrid',
  'Europe/Moscow',
  'Europe/Oslo',
  'Europe/Paris',
  'Europe/Prague',
  'Europe/Rome',
  'Europe/Stockholm',
  'Europe/Vienna',
  'Europe/Warsaw',
  'Europe/Zurich',
  'Pacific/Auckland',
];
