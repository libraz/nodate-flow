/**
 * @brief Shared Zod schema builders for slug + identifier (project key)
 *        fields used by the workspace and project create dialogs.
 *
 * Both dialogs apply the same lowercase slug rule and the same uppercase
 * 1..5 character identifier rule. Centralising the builders keeps the
 * validation message keys consistent and prevents the rules from
 * drifting.
 */
import { z } from 'zod';

export interface SlugFieldOptions {
  requiredKey: string;
  formatKey: string;
  maxLength?: number;
}

export interface IdentifierFieldOptions {
  requiredKey?: string;
  maxKey?: string;
  formatKey?: string;
  maxLength?: number;
}

/**
 * @brief Build a Zod schema for a URL-safe slug field.
 *
 * Lowercase letters, digits, and hyphens only. The translator key for
 * each rule is supplied by the caller because the surrounding namespace
 * differs (workspaces vs projects).
 *
 * The default maximum is 63 because a slug has to survive as a single
 * DNS label, which RFC 1035 caps at 63 octets. Both the workspace and
 * project slug columns are sized to that limit and the API rejects
 * anything longer, so the form has to stop at the same point to keep
 * the error inline instead of coming back as a 422.
 */
export function createSlugField(opts: SlugFieldOptions): z.ZodString {
  const max = opts.maxLength ?? 63;
  return z
    .string()
    .min(1, opts.requiredKey)
    .max(max)
    .regex(/^[a-z0-9-]+$/, opts.formatKey);
}

/**
 * @brief Build a Zod schema for an uppercase project identifier (key).
 *
 * Default keys live under the {@code labels} namespace because the
 * project create dialog already routes identifier errors through that
 * translator. Callers can override when integrating into a different
 * namespace.
 */
export function createIdentifierField(opts: IdentifierFieldOptions = {}): z.ZodString {
  const max = opts.maxLength ?? 5;
  return z
    .string()
    .min(1, opts.requiredKey ?? 'identifier.validation.required')
    .max(max, opts.maxKey ?? 'identifier.validation.max')
    .regex(/^[A-Za-z0-9]+$/, opts.formatKey ?? 'identifier.validation.format');
}
