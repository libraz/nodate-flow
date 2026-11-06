/** Return a normalized external HTTP(S) URL, rejecting inputs browsers may reinterpret. */
export function safeSourceUrl(value: string | null | undefined): string | undefined {
  if (!value || value !== value.trim() || /[\s\\]/u.test(value)) {
    return undefined;
  }
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code < 32 || code === 127) return undefined;
  }

  try {
    const url = new URL(value);
    if (
      (url.protocol !== 'http:' && url.protocol !== 'https:') ||
      url.username.length > 0 ||
      url.password.length > 0
    ) {
      return undefined;
    }
    return url.href;
  } catch {
    return undefined;
  }
}
