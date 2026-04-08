/**
 * Settings feature — typed Suspense queries and mutations for the
 * authenticated user's own profile (`GET /me` / `PATCH /me`).
 */

import type { components } from '@nodate-flow/sdk';
import {
  type UseMutationResult,
  type UseSuspenseQueryResult,
  useMutation,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';

import { sdk } from '../../lib/sdk';

export type Me = components['schemas']['MeBody'];
export type PatchMeInput = components['schemas']['PatchMeInputBody'];
export type SessionSummary = components['schemas']['SessionSummary'];

/** Query key factory for the settings feature. */
export const settingsKeys = {
  me: ['me'] as const,
  sessions: ['me', 'sessions'] as const,
};

/** Lightweight error thrown when the SDK returns an error envelope. */
export class SettingsApiError extends Error {
  readonly code: string | undefined;
  constructor(code: string | undefined, message: string) {
    super(message);
    this.name = 'SettingsApiError';
    this.code = code;
  }
}

function toError(err: unknown, fallback: string): SettingsApiError {
  if (err && typeof err === 'object') {
    const obj = err as { detail?: unknown; title?: unknown; type?: unknown };
    const message =
      (typeof obj.detail === 'string' && obj.detail) ||
      (typeof obj.title === 'string' && obj.title) ||
      fallback;
    const code = typeof obj.type === 'string' ? obj.type : undefined;
    return new SettingsApiError(code, message);
  }
  return new SettingsApiError(undefined, fallback);
}

/** GET /me — current authenticated user profile. */
export function useMeQuery(): UseSuspenseQueryResult<Me> {
  return useSuspenseQuery({
    queryKey: settingsKeys.me,
    queryFn: async (): Promise<Me> => {
      const { data, error } = await sdk.GET('/me');
      if (error || !data) throw toError(error, 'Failed to load profile');
      return data;
    },
  });
}

interface UpdateMeContext {
  previous: Me | undefined;
}

/**
 * PATCH /me — update profile fields with an optimistic cache update.
 * Rolls back to the previous snapshot on failure.
 */
export function useUpdateMe(): UseMutationResult<
  Me,
  SettingsApiError,
  PatchMeInput,
  UpdateMeContext
> {
  const qc = useQueryClient();
  return useMutation<Me, SettingsApiError, PatchMeInput, UpdateMeContext>({
    throwOnError: false,
    mutationFn: async (input: PatchMeInput): Promise<Me> => {
      const { data, error } = await sdk.PATCH('/me', { body: input });
      if (error || !data) throw toError(error, 'Failed to update profile');
      return data;
    },
    onMutate: async (input: PatchMeInput): Promise<UpdateMeContext> => {
      await qc.cancelQueries({ queryKey: settingsKeys.me });
      const previous = qc.getQueryData<Me>(settingsKeys.me);
      if (previous) {
        qc.setQueryData<Me>(settingsKeys.me, { ...previous, ...input });
      }
      return { previous };
    },
    onError: (_err, _input, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<Me>(settingsKeys.me, ctx.previous);
      }
    },
    onSuccess: (data) => {
      qc.setQueryData<Me>(settingsKeys.me, data);
    },
  });
}

/** GET /me/sessions — active sessions for the current user. */
export function useMySessionsQuery(): UseSuspenseQueryResult<SessionSummary[]> {
  return useSuspenseQuery({
    queryKey: settingsKeys.sessions,
    queryFn: async (): Promise<SessionSummary[]> => {
      const { data, error } = await sdk.GET('/me/sessions');
      if (error || !data) throw toError(error, 'Failed to load sessions');
      return data.items ?? [];
    },
  });
}

/** DELETE /me/sessions/{sessionId} — revoke a single session. */
export function useRevokeSession(): UseMutationResult<void, SettingsApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, SettingsApiError, string>({
    throwOnError: false,
    mutationFn: async (sessionId: string): Promise<void> => {
      const { error } = await sdk.DELETE('/me/sessions/{sessionId}', {
        params: { path: { sessionId } },
      });
      if (error) throw toError(error, 'Failed to revoke session');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.sessions });
    },
  });
}

/* ---------- TOTP 2FA ---------- */

export type TotpStatus = 'disabled' | 'pending' | 'enabled';

export const totpKeys = {
  status: ['me', 'totp'] as const,
  recovery: ['me', 'totp', 'recovery'] as const,
};

export function useTotpStatusQuery(): UseSuspenseQueryResult<TotpStatus> {
  return useSuspenseQuery({
    queryKey: totpKeys.status,
    queryFn: async (): Promise<TotpStatus> => {
      const { data, error } = await sdk.GET('/me/totp');
      if (error || !data) throw toError(error, 'Failed to load 2FA status');
      return data.status;
    },
  });
}

export interface TotpEnrollResponse {
  otpauthUrl: string;
  secret: string;
}

export function useTotpEnroll(): UseMutationResult<TotpEnrollResponse, SettingsApiError, void> {
  const qc = useQueryClient();
  return useMutation<TotpEnrollResponse, SettingsApiError, void>({
    throwOnError: false,
    mutationFn: async (): Promise<TotpEnrollResponse> => {
      const { data, error } = await sdk.POST('/me/totp/enroll');
      if (error || !data) throw toError(error, 'Failed to start 2FA enrollment');
      return { otpauthUrl: data.otpauthUrl, secret: data.secret };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.status });
    },
  });
}

export interface TotpConfirmResponse {
  recoveryCodes: string[];
}

export function useTotpConfirm(): UseMutationResult<TotpConfirmResponse, SettingsApiError, string> {
  const qc = useQueryClient();
  return useMutation<TotpConfirmResponse, SettingsApiError, string>({
    throwOnError: false,
    mutationFn: async (code: string): Promise<TotpConfirmResponse> => {
      const { data, error } = await sdk.POST('/me/totp/confirm', { body: { code } });
      if (error || !data) throw toError(error, 'Failed to confirm 2FA code');
      return { recoveryCodes: data.recoveryCodes ?? [] };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.status });
      void qc.invalidateQueries({ queryKey: totpKeys.recovery });
    },
  });
}

export function useRecoveryCodesStatusQuery(): UseSuspenseQueryResult<number> {
  return useSuspenseQuery({
    queryKey: totpKeys.recovery,
    queryFn: async (): Promise<number> => {
      const { data, error } = await sdk.GET('/me/totp/recovery-codes');
      if (error || !data) throw toError(error, 'Failed to load recovery code status');
      return data.remaining;
    },
  });
}

export function useRegenerateRecoveryCodes(): UseMutationResult<
  TotpConfirmResponse,
  SettingsApiError,
  string
> {
  const qc = useQueryClient();
  return useMutation<TotpConfirmResponse, SettingsApiError, string>({
    throwOnError: false,
    mutationFn: async (password: string): Promise<TotpConfirmResponse> => {
      const { data, error } = await sdk.POST('/me/totp/recovery-codes', { body: { password } });
      if (error || !data) throw toError(error, 'Failed to regenerate recovery codes');
      return { recoveryCodes: data.recoveryCodes ?? [] };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.recovery });
    },
  });
}

export function useTotpDisable(): UseMutationResult<void, SettingsApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, SettingsApiError, string>({
    throwOnError: false,
    mutationFn: async (password: string): Promise<void> => {
      const { error } = await sdk.DELETE('/me/totp', { body: { password } });
      if (error) throw toError(error, 'Failed to disable 2FA');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.status });
    },
  });
}

/** POST /me/password — change the current user's password. */
export interface ChangePasswordRequest {
  currentPassword: string;
  newPassword: string;
}
export interface ChangePasswordResponse {
  otherSessionsRevoked: number;
}
export function useChangePassword(): UseMutationResult<
  ChangePasswordResponse,
  SettingsApiError,
  ChangePasswordRequest
> {
  const qc = useQueryClient();
  return useMutation<ChangePasswordResponse, SettingsApiError, ChangePasswordRequest>({
    throwOnError: false,
    mutationFn: async (input: ChangePasswordRequest): Promise<ChangePasswordResponse> => {
      const { data, error } = await sdk.POST('/me/password', { body: input });
      if (error || !data) throw toError(error, 'Failed to change password');
      return { otherSessionsRevoked: data.otherSessionsRevoked };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.sessions });
    },
  });
}

/** DELETE /me/sessions — revoke every other session except the current one. */
export function useRevokeAllOtherSessions(): UseMutationResult<
  { revoked: number },
  SettingsApiError,
  void
> {
  const qc = useQueryClient();
  return useMutation<{ revoked: number }, SettingsApiError, void>({
    throwOnError: false,
    mutationFn: async (): Promise<{ revoked: number }> => {
      const { data, error } = await sdk.DELETE('/me/sessions');
      if (error || !data) throw toError(error, 'Failed to revoke sessions');
      return { revoked: data.revoked };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.sessions });
    },
  });
}
