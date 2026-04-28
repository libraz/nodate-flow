/**
 * Shared OAuth provider button row used by both /login and /signup.
 *
 * Reads {@link useCapabilities}'s `oidc*` flags to render at most one
 * button per enabled provider. The divider above the row mirrors the
 * styling that previously lived inline in {@code login.tsx} so the
 * extracted component preserves the original visual contract.
 *
 * The provider list and labels are identical between modes; only the
 * post-success flow differs (login resolves `?redirect=`, signup lands
 * on /profile after the OIDC callback completes server-side). The
 * difference is therefore purely a copy concern -- we do not branch on
 * `mode` for the network call itself, since the auth-api end-point is
 * symmetric.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { ProblemJson } from '../../lib/api-error';
import { type AuthErrorI18nKey, mapAuthError, mapAuthThrown } from '../../lib/auth-errors';
import { sdk } from '../../lib/sdk';
import { useCapabilities } from '../auth/use-capabilities';

/**
 * Operating mode of the row.
 *
 * Today both modes call the same `/auth/oidc/{provider}/start` endpoint,
 * so the prop only documents the caller's intent and is reserved for a
 * future divergence (e.g. analytics labelling). Keep it required so we
 * cannot accidentally drop the discriminator if a future provider
 * needs different flags.
 */
export type OAuthMode = 'login' | 'signup';

/** Supported OIDC providers, mirrored from the auth-api capabilities flags. */
export type OAuthProvider = 'google' | 'github' | 'microsoft';

export interface OAuthButtonRowProps {
  /** Discriminator -- which page this row is rendered on. */
  mode: OAuthMode;
  /**
   * Optional callback so the parent can surface the auth-api error in
   * its own banner. The component does not render its own error UI.
   */
  onError?: (key: AuthErrorI18nKey) => void;
}

/**
 * Renders the OAuth provider buttons + divider for sign-in / sign-up.
 *
 * Returns `null` when capabilities have not loaded yet or when no OIDC
 * provider is enabled, so the caller can drop it in unconditionally.
 */
function OAuthButtonRow({ mode, onError }: OAuthButtonRowProps): ReactElement | null {
  const { t } = useTranslation('auth');
  const caps = useCapabilities();
  // Tracks the in-flight provider so we can disable every button while a
  // start call is pending. We do not use `useSubmitGuard()` here because
  // the success path navigates via `window.location` and never releases
  // the guard; a thrown / errored start call still needs to flip back to
  // the idle UX so the user can retry. A nullable provider id captures
  // both pieces (which one is busy + whether anything is busy).
  const [pendingProvider, setPendingProvider] = useState<OAuthProvider | null>(null);

  // Caps are still loading -- defer rendering until we know whether any
  // provider is actually enabled. A flicker of the divider would be
  // worse than a tiny delay here since this row is below the fold.
  if (!caps) return null;
  if (!caps.oidcGoogle && !caps.oidcGithub && !caps.oidcMicrosoft) return null;

  // Tracking aria-busy independently lets us drop the busy hint *before*
  // flipping `pendingProvider` back to null on error so screen readers do
  // not briefly announce a busy-but-enabled control as React replays the
  // two state updates.
  const [busyProvider, setBusyProvider] = useState<OAuthProvider | null>(null);

  const handleStart = async (provider: OAuthProvider): Promise<void> => {
    if (pendingProvider !== null) return;
    setPendingProvider(provider);
    setBusyProvider(provider);
    try {
      const { data, error } = await sdk.GET(`/auth/oidc/${provider}/start` as never);
      if (error || !data) {
        onError?.(mapAuthError(error as ProblemJson | undefined));
        setBusyProvider(null);
        setPendingProvider(null);
        return;
      }
      const result = data as { authorizationUrl: string };
      // Leave `pendingProvider` set on success: the browser is about to
      // navigate away, so re-enabling the buttons would only flash a
      // brief idle UX and re-open the multi-click race we are guarding
      // against.
      window.location.href = result.authorizationUrl;
    } catch (err) {
      onError?.(mapAuthThrown(err));
      setBusyProvider(null);
      setPendingProvider(null);
    }
  };

  const isPending = pendingProvider !== null;

  // `data-mode` is a stable hook for parent CSS / E2E selectors but
  // does not influence rendering today.
  return (
    <div data-mode={mode}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--nf-space-3)',
          color: 'var(--nf-color-fg-muted)',
          fontSize: 'var(--nf-text-sm)',
        }}
      >
        <hr style={{ flex: 1, border: 'none', borderTop: '1px solid var(--nf-color-border)' }} />
        <span>{t('login.sso_divider')}</span>
        <hr style={{ flex: 1, border: 'none', borderTop: '1px solid var(--nf-color-border)' }} />
      </div>

      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
          marginTop: 'var(--nf-space-3)',
        }}
      >
        {caps.oidcGoogle && (
          <Button
            type="button"
            variant="default"
            disabled={isPending}
            aria-busy={busyProvider === 'google'}
            onClick={() => {
              void handleStart('google');
            }}
          >
            {t('login.sso_google')}
          </Button>
        )}
        {caps.oidcGithub && (
          <Button
            type="button"
            variant="default"
            disabled={isPending}
            aria-busy={busyProvider === 'github'}
            onClick={() => {
              void handleStart('github');
            }}
          >
            {t('login.sso_github')}
          </Button>
        )}
        {caps.oidcMicrosoft && (
          <Button
            type="button"
            variant="default"
            disabled={isPending}
            aria-busy={busyProvider === 'microsoft'}
            onClick={() => {
              void handleStart('microsoft');
            }}
          >
            {t('login.sso_microsoft')}
          </Button>
        )}
      </div>
    </div>
  );
}

export default OAuthButtonRow;
