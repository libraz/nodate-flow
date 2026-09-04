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

import { authApiRequest } from '../../lib/api';

export type Me = components['schemas']['MeBody'];
export type PatchMeInput = components['schemas']['PatchMeInputBody'];
export type SessionSummary = components['schemas']['SessionSummary'];

/** Query key factory for the settings feature. */
export const settingsKeys = {
  me: ['me'] as const,
  sessions: ['me', 'sessions'] as const,
};

import { ApiError } from '../../lib/api-error';

export { ApiError as SettingsApiError };

/** GET /me — current authenticated user profile. */
export function useMeQuery(): UseSuspenseQueryResult<Me> {
  return useSuspenseQuery({
    queryKey: settingsKeys.me,
    queryFn: async (): Promise<Me> => {
      const data = await authApiRequest((client) => client.GET('/me'), 'Failed to load profile');
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
export function useUpdateMe(): UseMutationResult<Me, ApiError, PatchMeInput, UpdateMeContext> {
  const qc = useQueryClient();
  return useMutation<Me, ApiError, PatchMeInput, UpdateMeContext>({
    throwOnError: false,
    mutationFn: async (input: PatchMeInput): Promise<Me> => {
      const data = await authApiRequest(
        (client) => client.PATCH('/me', { body: input }),
        'Failed to update profile',
      );
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
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.me });
    },
  });
}

/** GET /me/sessions — active sessions for the current user. */
export function useMySessionsQuery(): UseSuspenseQueryResult<SessionSummary[]> {
  return useSuspenseQuery({
    queryKey: settingsKeys.sessions,
    queryFn: async (): Promise<SessionSummary[]> => {
      const data = await authApiRequest(
        (client) => client.GET('/me/sessions'),
        'Failed to load sessions',
      );
      return data.items ?? [];
    },
  });
}

/** DELETE /me/sessions/{sessionId} — revoke a single session. */
export function useRevokeSession(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    throwOnError: false,
    mutationFn: async (sessionId: string): Promise<void> => {
      await authApiRequest(
        (client) =>
          client.DELETE('/me/sessions/{sessionId}', {
            params: { path: { sessionId } },
          }),
        'Failed to revoke session',
      );
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
      const data = await authApiRequest(
        (client) => client.GET('/me/totp'),
        'Failed to load 2FA status',
      );
      return data.status;
    },
  });
}

export type TotpEnrollResponse = Pick<
  components['schemas']['TotpEnrollOutputBody'],
  'otpauthUrl' | 'secret'
>;

export function useTotpEnroll(): UseMutationResult<TotpEnrollResponse, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<TotpEnrollResponse, ApiError, string>({
    throwOnError: false,
    mutationFn: async (password: string): Promise<TotpEnrollResponse> => {
      const data = await authApiRequest(
        (client) => client.POST('/me/totp/enroll', { body: { password } }),
        'Failed to start 2FA enrollment',
      );
      return { otpauthUrl: data.otpauthUrl, secret: data.secret };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.status });
    },
  });
}

/**
 * Recovery codes as the panel consumes them. Not a mirror of one API
 * schema: `/me/totp/confirm` and `/me/totp/recovery-codes` each return
 * `recoveryCodes` as nullable, and both hooks below normalise the null
 * away so callers can render the list without a second empty-state.
 */
export interface RecoveryCodesResult {
  recoveryCodes: string[];
}

export interface TotpConfirmRequest {
  code: string;
  password: string;
}

export function useTotpConfirm(): UseMutationResult<
  RecoveryCodesResult,
  ApiError,
  TotpConfirmRequest
> {
  const qc = useQueryClient();
  return useMutation<RecoveryCodesResult, ApiError, TotpConfirmRequest>({
    throwOnError: false,
    mutationFn: async ({ code, password }: TotpConfirmRequest): Promise<RecoveryCodesResult> => {
      const data = await authApiRequest(
        (client) => client.POST('/me/totp/confirm', { body: { code, password } }),
        'Failed to confirm 2FA code',
      );
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
      const data = await authApiRequest(
        (client) => client.GET('/me/totp/recovery-codes'),
        'Failed to load recovery code status',
      );
      return data.remaining;
    },
  });
}

export function useRegenerateRecoveryCodes(): UseMutationResult<
  RecoveryCodesResult,
  ApiError,
  string
> {
  const qc = useQueryClient();
  return useMutation<RecoveryCodesResult, ApiError, string>({
    throwOnError: false,
    mutationFn: async (password: string): Promise<RecoveryCodesResult> => {
      const data = await authApiRequest(
        (client) => client.POST('/me/totp/recovery-codes', { body: { password } }),
        'Failed to regenerate recovery codes',
      );
      return { recoveryCodes: data.recoveryCodes ?? [] };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: totpKeys.recovery });
    },
  });
}

export function useTotpDisable(): UseMutationResult<void, ApiError, string> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, string>({
    throwOnError: false,
    mutationFn: async (password: string): Promise<void> => {
      await authApiRequest(
        (client) => client.DELETE('/me/totp', { body: { password } }),
        'Failed to disable 2FA',
      );
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
export type ChangePasswordResponse = Pick<
  components['schemas']['ChangePasswordOutputBody'],
  'otherSessionsRevoked'
>;
export function useChangePassword(): UseMutationResult<
  ChangePasswordResponse,
  ApiError,
  ChangePasswordRequest
> {
  const qc = useQueryClient();
  return useMutation<ChangePasswordResponse, ApiError, ChangePasswordRequest>({
    throwOnError: false,
    mutationFn: async (input: ChangePasswordRequest): Promise<ChangePasswordResponse> => {
      const data = await authApiRequest(
        (client) => client.POST('/me/password', { body: input }),
        'Failed to change password',
      );
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
  ApiError,
  void
> {
  const qc = useQueryClient();
  return useMutation<{ revoked: number }, ApiError, void>({
    throwOnError: false,
    mutationFn: async (): Promise<{ revoked: number }> => {
      const data = await authApiRequest(
        (client) => client.DELETE('/me/sessions'),
        'Failed to revoke sessions',
      );
      return { revoked: data.revoked };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.sessions });
    },
  });
}
