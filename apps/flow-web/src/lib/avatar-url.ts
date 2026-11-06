import { authApiBaseUrl } from './sdk';

/** Resolve an avatar URL from the auth API for use as an image source. */
export function resolveAvatarUrl(
  serverUrl: string | null | undefined,
  baseUrl: string = authApiBaseUrl,
): string | undefined {
  if (!serverUrl) return undefined;
  if (/^https?:\/\//i.test(serverUrl)) return serverUrl;
  if (!serverUrl.startsWith('/') || serverUrl.startsWith('//')) return undefined;

  try {
    const fallbackOrigin =
      typeof window === 'undefined' ? 'http://localhost' : window.location.origin;
    const base = new URL(baseUrl, fallbackOrigin);
    const avatar = new URL(serverUrl, base.origin);
    const basePath = base.pathname.replace(/\/+$/, '');
    base.pathname = `${basePath}${avatar.pathname}`;
    avatar.searchParams.forEach((value, key) => {
      base.searchParams.set(key, value);
    });
    if (avatar.hash) base.hash = avatar.hash;
    return base.toString();
  } catch {
    return undefined;
  }
}
