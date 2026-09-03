import type { components } from '@nodate-flow/sdk';
import { useEffect, useState } from 'react';
import { apiRequest } from '../../lib/api';

/** Boolean flags describing which auth methods are available. */
export type AuthCapabilities = components['schemas']['CapabilitiesBody'];

/** Conservative default: only password login shown until the server responds. */
const defaultCaps: AuthCapabilities = {
  passwordLogin: true,
  oidcGoogle: false,
  oidcGithub: false,
  oidcMicrosoft: false,
  magicLink: false,
  totp: false,
  registrationOpen: true,
};

// Module-level cache so only one fetch happens per page load.
let cached: AuthCapabilities | null = null;

/**
 * Fetches auth capabilities once (GET /auth/capabilities) and caches
 * the result for the lifetime of the page. Returns null while loading.
 */
export function useCapabilities(): AuthCapabilities | null {
  const [caps, setCaps] = useState<AuthCapabilities | null>(cached);

  useEffect(() => {
    if (cached) return;
    let cancelled = false;
    // A server that will not say which sign-in methods exist leaves the
    // conservative default in place: password only, which every
    // deployment supports.
    void apiRequest(
      (client) => client.GET('/auth/capabilities'),
      'Failed to load auth capabilities',
      { onError: 'empty', empty: defaultCaps },
    ).then((result) => {
      if (cancelled) return;
      const caps = result ?? defaultCaps;
      cached = caps;
      setCaps(caps);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return caps;
}
