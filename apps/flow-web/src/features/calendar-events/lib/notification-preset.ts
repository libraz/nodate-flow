/** Notification preset identifiers. `none` omits the field on submit. */
export type NotificationPreset =
  | 'none'
  | 'at_time'
  | '5min'
  | '10min'
  | '15min'
  | '30min'
  | '1hour'
  | '1day';

/** Translate a notification preset to minutes (null = omit from payload). */
export function presetToMinutes(preset: NotificationPreset): number | null {
  switch (preset) {
    case 'none':
      return null;
    case 'at_time':
      return 0;
    case '5min':
      return 5;
    case '10min':
      return 10;
    case '15min':
      return 15;
    case '30min':
      return 30;
    case '1hour':
      return 60;
    case '1day':
      return 24 * 60;
  }
}
