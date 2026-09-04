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
 * @brief Maximum length of a slug, in characters.
 *
 * A slug has to survive as a single DNS label, which RFC 1035 caps at 63
 * octets. Both the workspace and project slug columns are sized to that
 * limit and the API rejects anything longer, so the form has to stop at
 * the same point to keep the error inline instead of coming back as a
 * 422. Generation ({@link slugify}) and validation
 * ({@link createSlugField}) both read this constant so a generated slug
 * can never exceed what the schema accepts.
 */
export const DNS_LABEL_MAX_LENGTH = 63;

/**
 * @brief Build a Zod schema for a URL-safe slug field.
 *
 * Lowercase letters, digits, and hyphens only. The translator key for
 * each rule is supplied by the caller because the surrounding namespace
 * differs (workspaces vs projects).
 */
export function createSlugField(opts: SlugFieldOptions): z.ZodString {
  const max = opts.maxLength ?? DNS_LABEL_MAX_LENGTH;
  return z
    .string()
    .min(1, opts.requiredKey)
    .max(max)
    .regex(/^[a-z0-9-]+$/, opts.formatKey);
}

/**
 * @brief Derive a slug-safe string from a display name.
 *
 * Lowercase, spaces and punctuation collapse to a single '-', the result
 * is truncated to {@link DNS_LABEL_MAX_LENGTH}, and only then are edge
 * dashes dropped: truncating first can cut just after an interior
 * hyphen, and the API allows hyphens only between alphanumerics. Used to
 * prefill the slug field while the user types a name, so its output has
 * to be a value the API will accept.
 */
export function slugify(input: string): string {
  return input
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .slice(0, DNS_LABEL_MAX_LENGTH)
    .replace(/^-+|-+$/g, '');
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
