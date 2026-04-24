/**
 * Thin fetch wrappers for the auth-api avatar endpoints.
 *
 * Multipart/form-data cannot round-trip cleanly through the
 * openapi-fetch SDK, so this module talks to {@code POST /me/avatar} and
 * {@code DELETE /me/avatar} directly. It replicates the token-refresh
 * handshake used by the SDK middleware (proactive refresh when the
 * bearer is near expiry, plus {@code credentials: 'include'} so the
 * httpOnly refresh cookie rides along) and normalises failures into
 * the shared {@link ApiError} shape.
 */

import type { components } from '@nodate-flow/sdk';

import { toApiError } from '../../lib/api-error';
import { authApiBaseUrl, refreshAccessToken } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';

export type Me = components['schemas']['MeBody'];

/**
 * Resolve a valid {@code Authorization} header value, proactively
 * refreshing the access token when it is close to expiry. Throws when
 * no session is available (the mutation surface translates this into
 * a generic failure toast).
 */
async function authHeader(): Promise<string> {
  if (refreshAccessToken.isExpiringSoon()) {
    await refreshAccessToken();
  }
  const token = authStore.getState().accessToken;
  if (!token) {
    throw toApiError(null, 'Not authenticated', 401);
  }
  return `Bearer ${token}`;
}

/**
 * Upload a profile picture. Returns the updated {@link Me} body whose
 * {@code avatarUrl} now points at the proxy URL (with a fresh
 * {@code ?v=} cache-bust token).
 */
export async function uploadAvatar(file: File): Promise<Me> {
  const form = new FormData();
  form.append('file', file);
  const authorization = await authHeader();
  const res = await fetch(`${authApiBaseUrl}/me/avatar`, {
    method: 'POST',
    credentials: 'include',
    headers: { authorization },
    body: form,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw toApiError(body, 'Avatar upload failed', res.status);
  }
  return (await res.json()) as Me;
}

/**
 * Remove the current profile picture. Returns the updated {@link Me}
 * body with {@code avatarUrl} unset.
 */
export async function deleteAvatar(): Promise<Me> {
  const authorization = await authHeader();
  const res = await fetch(`${authApiBaseUrl}/me/avatar`, {
    method: 'DELETE',
    credentials: 'include',
    headers: { authorization },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    throw toApiError(body, 'Avatar delete failed', res.status);
  }
  return (await res.json()) as Me;
}
