/**
 * Avatar URL helpers shared across frontend apps.
 *
 * The auth-api hosts a public proxy endpoint at `/avatars/{userId}` that
 * resolves the canonical avatar source for a given user public id and
 * applies its own Cache-Control headers. This module centralises how
 * frontends construct that URL so callers never hand-craft the path.
 */

/**
 * @brief Build a proxy URL for the user avatar served by auth-api.
 *
 * The auth-api `/avatars/{userId}` endpoint is public and applies its own
 * Cache-Control headers. Use this when you have a userId but no avatarUrl
 * in hand (e.g., a row only carries actor_user_id). When the API already
 * returns `avatarUrl`, prefer that field directly so the cache buster
 * appended by the server (`?v=...`) survives.
 *
 * @param userId         User public id (UUID v7).
 * @param authApiBaseUrl Resolved base URL of the auth-api service. Trailing
 *                       slashes are tolerated.
 * @returns Absolute URL pointing at the auth-api avatar proxy endpoint.
 */
export function buildAvatarUrl(userId: string, authApiBaseUrl: string): string {
  return `${authApiBaseUrl.replace(/\/$/, '')}/avatars/${encodeURIComponent(userId)}`;
}
