/**
 * @brief Format a byte count as a localized "value unit" string.
 * @param bytes Raw byte count (>= 0).
 * @param locale BCP 47 locale tag used for number formatting.
 * @param t Translator bound to the {@code common} namespace; reads
 *          {@code file_size.*} keys.
 *
 * Walks the binary KiB ladder until the value falls under 1024 or the
 * largest known unit (TB) is reached. Bytes never get a fractional
 * digit; larger units get one.
 */
export function formatBytes(bytes: number, locale: string, t: (key: string) => string): string {
  const unitKeys = [
    'file_size.byte',
    'file_size.kilobyte',
    'file_size.megabyte',
    'file_size.gigabyte',
    'file_size.terabyte',
  ] as const;
  let value = bytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < unitKeys.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const formatted = new Intl.NumberFormat(locale, {
    maximumFractionDigits: unitIndex === 0 ? 0 : 1,
  }).format(value);
  const unitKey = unitKeys[unitIndex] ?? unitKeys[0];
  return `${formatted} ${t(unitKey)}`;
}
