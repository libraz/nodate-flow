/**
 * Zod schemas for authentication forms. These live inside the feature
 * folder per project convention (no shared schema package).
 *
 * Validation messages are i18n keys resolved at render time via `t()`.
 */

import { z } from 'zod';

export const loginSchema = z.object({
  email: z.string().min(1, 'auth.validation.email_required').email('auth.validation.email_invalid'),
  password: z.string().min(8, 'auth.validation.password_min'),
});

export type LoginFormValues = z.infer<typeof loginSchema>;

export const signupSchema = z.object({
  email: z.string().min(1, 'auth.validation.email_required').email('auth.validation.email_invalid'),
  password: z.string().min(8, 'auth.validation.password_min'),
  displayName: z.string().min(1, 'auth.validation.name_required'),
});

export type SignupFormValues = z.infer<typeof signupSchema>;

export const changePasswordSchema = z.object({
  currentPassword: z.string().min(1, 'auth.validation.current_password_required'),
  newPassword: z.string().min(8, 'auth.validation.password_min'),
});

export type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

export const profileSchema = z.object({
  displayName: z.string().min(1, 'auth.validation.name_required'),
  locale: z.enum(['en', 'ja']),
  themePreference: z.enum([
    'aurora-light',
    'aurora-dark',
    'dotline-light',
    'dotline-dark',
    'glass-light',
    'glass-dark',
    'system',
  ]),
});

export type ProfileFormValues = z.infer<typeof profileSchema>;
