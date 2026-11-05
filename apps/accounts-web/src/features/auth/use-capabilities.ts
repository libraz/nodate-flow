import type { components } from '@nodate-flow/sdk';
import { useCallback, useEffect, useState } from 'react';
import { apiRequest } from '../../lib/api';

/** Boolean flags describing which auth methods are available. */
export type AuthCapabilities = components['schemas']['CapabilitiesBody'];

/**
 * Where the capabilities request stands. Screens render the sections that
 * depend on the flags only in the `ready` state: while `loading` they show
 * nothing, and on `error` they offer `retry`.
 */
export type CapabilitiesState =
  | { status: 'loading' }
  | { status: 'error'; retry: () => void }
  | { status: 'ready'; caps: AuthCapabilities };

// Module-level cache so only one fetch happens per page load.
let cached: AuthCapabilities | null = null;
// The request in flight, shared by every hook instance mounted meanwhile.
let inflight: Promise<AuthCapabilities> | null = null;

function fetchCapabilities(): Promise<AuthCapabilities> {
  inflight ??= apiRequest(
    (client) => client.GET('/auth/capabilities'),
    'Failed to load auth capabilities',
  )
    .then((caps) => {
      cached = caps;
      return caps;
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

/** Forgets the cached result. Tests only. */
export function resetCapabilitiesCache(): void {
  cached = null;
  inflight = null;
}

/**
 * Fetches auth capabilities once (GET /auth/capabilities) and caches
 * the result for the lifetime of the page.
 */
export function useCapabilities(): CapabilitiesState {
  const [state, setState] = useState<CapabilitiesState>(
    cached ? { status: 'ready', caps: cached } : { status: 'loading' },
  );
  const [attempt, setAttempt] = useState(0);

  const retry = useCallback((): void => {
    setState({ status: 'loading' });
    setAttempt((n) => n + 1);
  }, []);

  // `attempt` re-runs the request when the user asks for a retry.
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt is a re-run trigger, not a value the effect reads
  useEffect(() => {
    if (cached) {
      setState({ status: 'ready', caps: cached });
      return;
    }
    let cancelled = false;
    fetchCapabilities().then(
      (caps) => {
        if (!cancelled) setState({ status: 'ready', caps });
      },
      () => {
        if (!cancelled) setState({ status: 'error', retry });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [attempt, retry]);

  return state;
}

/** The capabilities once known, `null` while loading or after a failure. */
export function capsOf(state: CapabilitiesState): AuthCapabilities | null {
  return state.status === 'ready' ? state.caps : null;
}
