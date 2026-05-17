/**
 * theme-provider — server sync target verification.
 *
 * Regression guard for the bug where the flow-web ThemeProvider issued
 * `PATCH /me` against the flow-api SDK (port 8080), which has no `/me`
 * route, instead of the auth-api SDK (port 8082). The test mocks both
 * SDK clients and asserts the `authSdk` PATCH path is invoked, never the
 * product `sdk`.
 */

import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({
  productPatch: vi.fn(),
  authPatch: vi.fn(),
}));

vi.mock('../../lib/sdk', () => ({
  sdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    PATCH: sdkMocks.productPatch,
  },
  authSdk: {
    // biome-ignore lint/style/useNamingConvention: openapi-fetch HTTP method key
    PATCH: sdkMocks.authPatch,
  },
}));

import { authStore } from '@nodate-flow/sdk';

import { ThemeProvider, useTheme } from '../theme-provider';

beforeEach(() => {
  sdkMocks.productPatch.mockReset();
  sdkMocks.authPatch.mockReset();
  // Default: succeed with an empty Me payload.
  sdkMocks.authPatch.mockResolvedValue({ data: {}, error: null });
  // Provide an access token so syncServerTheme does not bail early.
  authStore.getState().setAccessToken('test-token');
  // Reset the theme key so the provider boots into a known state.
  try {
    window.localStorage.removeItem('nf.theme');
  } catch {
    /* ignore */
  }
});

afterEach(() => {
  authStore.getState().clearSession();
  try {
    window.localStorage.removeItem('nf.theme');
  } catch {
    /* ignore */
  }
  vi.clearAllMocks();
});

/** Tiny consumer that exposes `setPreference` to the test body. */
function ThemeProbe({
  capture,
}: {
  capture: (setPref: (p: 'aurora-dark' | 'aurora-light') => void) => void;
}): null {
  const { setPreference } = useTheme();
  capture(setPreference);
  return null;
}

describe('ThemeProvider syncServerTheme target', () => {
  it('PATCHes /me on authSdk (auth-api), not the product sdk (flow-api)', async () => {
    let setPref: ((p: 'aurora-dark' | 'aurora-light') => void) | null = null;
    render(
      <ThemeProvider>
        <ThemeProbe
          capture={(fn) => {
            setPref = fn;
          }}
        />
      </ThemeProvider>,
    );
    expect(setPref, 'consumer must have rendered and captured setPreference').not.toBeNull();

    // Trigger a preference change. The first render is intentionally
    // skipped by the provider (`isFirstRender`), so a single explicit
    // change is what causes the sync to fire.
    await act(async () => {
      setPref?.('aurora-dark');
      // Yield once so the fire-and-forget promise resolves before assertions.
      await Promise.resolve();
    });

    expect(sdkMocks.authPatch).toHaveBeenCalledTimes(1);
    expect(sdkMocks.authPatch).toHaveBeenCalledWith('/me', {
      body: { themePreference: 'aurora-dark' },
    });
    expect(sdkMocks.productPatch).not.toHaveBeenCalled();
  });
});
