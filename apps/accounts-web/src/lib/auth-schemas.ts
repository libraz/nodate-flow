/**
 * Zod schemas for authentication forms. These live inside the lib
 * folder per project convention (no shared schema package).
 *
 * Validation messages are i18n keys resolved at render time via `t()`.
 */

import { z } from 'zod';

export const loginSchema = z.object({
  email: z.string().min(1, 'auth.validation.emailRequired').email('auth.validation.emailInvalid'),
  password: z.string().min(8, 'auth.validation.passwordMin'),
});

export type LoginFormValues = z.infer<typeof loginSchema>;

export const signupSchema = z.object({
  email: z.string().min(1, 'auth.validation.emailRequired').email('auth.validation.emailInvalid'),
  password: z.string().min(8, 'auth.validation.passwordMin'),
  displayName: z.string().min(1, 'auth.validation.nameRequired'),
});

export type SignupFormValues = z.infer<typeof signupSchema>;

export const changePasswordSchema = z.object({
  currentPassword: z.string().min(1, 'auth.validation.currentPasswordRequired'),
  newPassword: z.string().min(8, 'auth.validation.passwordMin'),
});

export type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

export const profileSchema = z.object({
  displayName: z.string().min(1, 'auth.validation.nameRequired'),
  locale: z.enum(['en', 'ja']),
  themePreference: z.enum([
    'aurora-light',
    'aurora-dark',
    'dotline-light',
    'dotline-dark',
    'system',
  ]),
});

export type ProfileFormValues = z.infer<typeof profileSchema>;
