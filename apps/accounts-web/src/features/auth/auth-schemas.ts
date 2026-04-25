/**
 * Zod schemas for authentication forms. These live inside the feature
 * folder per project convention (no shared schema package).
 *
 * Validation messages are i18n keys resolved at render time via `t()`.
 * Every call site uses `useTranslation('auth')`, so keys here omit the
 * `auth.` namespace prefix — i18next already resolves them inside the
 * `auth` ns (e.g. `validation.email_required` → `auth.json → validation
 * → email_required`). Prefixing with `auth.` would make i18next look up
 * `auth.json → auth → validation → email_required`, which does not
 * exist and surfaces the raw key in the UI.
 */

import { z } from 'zod';

export const loginSchema = z.object({
  email: z.string().min(1, 'validation.email_required').email('validation.email_invalid'),
  password: z.string().min(8, 'validation.password_min'),
});

export type LoginFormValues = z.infer<typeof loginSchema>;

export const signupSchema = z.object({
  email: z.string().min(1, 'validation.email_required').email('validation.email_invalid'),
  password: z.string().min(8, 'validation.password_min'),
  displayName: z.string().min(1, 'validation.name_required'),
});

export type SignupFormValues = z.infer<typeof signupSchema>;

export const changePasswordSchema = z.object({
  currentPassword: z.string().min(1, 'validation.current_password_required'),
  newPassword: z.string().min(8, 'validation.password_min'),
});

export type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

export const profileSchema = z.object({
  displayName: z.string().min(1, 'validation.name_required'),
  locale: z.enum(['en', 'ja', 'zh']),
  timezone: z.string().min(1, 'validation.timezone_required'),
  country: z
    .string()
    .regex(/^([A-Z]{2})?$/, 'validation.country_invalid')
    .or(z.literal('')),
  themePreference: z.enum([
    'aurora-light',
    'aurora-dark',
    'dotline-light',
    'dotline-dark',
    'glass-light',
    'glass-dark',
    'system',
  ]),
  // Mirrors the `MeBody.weekStart` enum from the auth-api OpenAPI spec.
  weekStart: z.enum(['mon', 'sun', 'sat']),
});

export type ProfileFormValues = z.infer<typeof profileSchema>;
