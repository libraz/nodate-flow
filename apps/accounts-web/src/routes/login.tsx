/**
 * /login -- public page. Redirects to `?redirect=` target or / when
 * already authenticated.
 *
 * Uses React Hook Form + Zod for client-side validation. The TOTP
 * challenge step remains useState-based because it is a secondary
 * step triggered by the server, not a user-filled form.
 */

import type { components } from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { createFileRoute, Link, useNavigate, useSearch } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import AuthCard from '../components/auth-card';
import { type LoginFormValues, loginSchema } from '../features/auth/auth-schemas';
import AuthStep from '../features/auth/auth-step';
import { authStore, selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import PasswordInput from '../features/auth/password-input';
import TotpChallengeForm, { type TotpCredential } from '../features/auth/totp-challenge-form';
import { useCapabilities } from '../features/auth/use-capabilities';
import { useRateLimitCountdown } from '../features/auth/use-rate-limit-countdown';
import { type MeResponse, userFromMe } from '../features/auth/user-from-me';
import OAuthButtonRow from '../features/oauth/oauth-button-row';
import { apiRequest, apiSettle } from '../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown } from '../lib/auth-errors';
import { safeRedirectTarget } from '../lib/redirect-target';
import { useSubmitGuard } from '../lib/use-submit-guard';

// Module-scope constant so Vite tree-shakes the dev-only panel out of
// production bundles when used as a JSX guard. A render-scope check would
// still ship the JSX and seed list to prod.
type LoginStep = 'password' | 'magic-request' | 'magic-sent' | 'totp';

const DEV_MODE = import.meta.env.DEV;

const DEV_SEED_PASSWORD = 'password123';

type DevRoleLabelKey =
  | 'login.devQuickLogin.roles.instanceAdmin'
  | 'login.devQuickLogin.roles.workspaceOwner'
  | 'login.devQuickLogin.roles.workspaceAdmin'
  | 'login.devQuickLogin.roles.workspaceMember'
  | 'login.devQuickLogin.roles.workspaceGuest'
  | 'login.devQuickLogin.roles.projectLead'
  | 'login.devQuickLogin.roles.projectViewer';

interface DevSeedRole {
  labelKey: DevRoleLabelKey;
  email: string;
  rank: 'hero' | 'owner' | 'admin' | 'member' | 'guest' | 'lead' | 'viewer';
}

const DEV_TIER_INSTANCE: readonly DevSeedRole[] = [
  { labelKey: 'login.devQuickLogin.roles.instanceAdmin', email: 'admin@example.com', rank: 'hero' },
];

const DEV_TIER_WORKSPACE: readonly DevSeedRole[] = [
  {
    labelKey: 'login.devQuickLogin.roles.workspaceOwner',
    email: 'owner@example.com',
    rank: 'owner',
  },
  {
    labelKey: 'login.devQuickLogin.roles.workspaceAdmin',
    email: 'wsadmin@example.com',
    rank: 'admin',
  },
  {
    labelKey: 'login.devQuickLogin.roles.workspaceMember',
    email: 'alice@example.com',
    rank: 'member',
  },
  {
    labelKey: 'login.devQuickLogin.roles.workspaceGuest',
    email: 'guest@example.com',
    rank: 'guest',
  },
];

const DEV_TIER_PROJECT: readonly DevSeedRole[] = [
  {
    labelKey: 'login.devQuickLogin.roles.projectLead',
    email: 'projectlead@example.com',
    rank: 'lead',
  },
  {
    labelKey: 'login.devQuickLogin.roles.projectViewer',
    email: 'viewer@example.com',
    rank: 'viewer',
  },
];

// Co-located styles. Lives inside the DEV_MODE-gated subtree so the
// constant-folded JSX drops the entire <style> tag from prod bundles.
const DEV_PANEL_STYLES = `
.dql-panel {
  display: flex;
  flex-direction: column;
  gap: var(--nf-space-3);
  padding: var(--nf-space-4);
  border: 1px dashed var(--nf-color-border);
  border-radius: var(--nf-radius-md);
  background: color-mix(in srgb, var(--nf-color-bg-sunken) 65%, transparent);
}
.dql-head {
  display: flex;
  align-items: center;
  gap: var(--nf-space-2);
}
.dql-tag {
  display: inline-flex;
  align-items: center;
  padding: var(--nf-space-0-5) var(--nf-space-1-5);
  border-radius: 3px;
  background: var(--nf-color-warning);
  color: var(--nf-color-fg-on-accent);
  font-family: var(--nf-font-mono);
  /* nf-token-override: a monospace tag set below the micro step on purpose — it is a marking on the demo panel, not text anyone reads as a word */
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.12em;
  line-height: 1;
  text-transform: uppercase;
}
.dql-title {
  margin: 0;
  font-size: var(--nf-text-sm);
  font-weight: var(--nf-weight-medium);
  color: var(--nf-color-fg);
}
.dql-tier {
  display: flex;
  flex-direction: column;
  gap: var(--nf-space-2);
}
.dql-tier-label {
  display: flex;
  align-items: center;
  gap: var(--nf-space-2);
  font-family: var(--nf-font-mono);
  /* nf-token-override: a wide-tracked uppercase tier label; the letter-spacing carries the legibility that the size gives up, so it does not follow the type scale */
  font-size: 10px;
  font-weight: var(--nf-weight-semibold);
  color: var(--nf-color-fg-subtle);
  text-transform: uppercase;
  letter-spacing: 0.14em;
}
.dql-tier-label::after {
  content: "";
  flex: 1;
  height: 1px;
  background: linear-gradient(90deg, var(--nf-color-border), transparent);
}
.dql-grid {
  display: grid;
  gap: var(--nf-space-2);
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.dql-chip {
  display: flex;
  align-items: center;
  gap: var(--nf-space-2);
  padding: var(--nf-space-2) var(--nf-space-3);
  border: 1px solid var(--nf-color-border);
  border-radius: var(--nf-radius-sm);
  background: var(--nf-color-bg-elevated);
  color: var(--nf-color-fg);
  font-family: inherit;
  font-size: var(--nf-text-sm);
  text-align: left;
  cursor: pointer;
  transition:
    background-color var(--nf-duration-fast) var(--nf-ease-standard),
    border-color var(--nf-duration-fast) var(--nf-ease-standard),
    transform var(--nf-duration-fast) var(--nf-ease-standard);
}
.dql-chip:hover:not(:disabled) {
  background: var(--nf-color-surface-hover);
  border-color: var(--nf-color-border-strong);
}
.dql-chip:active:not(:disabled) {
  transform: translateY(1px);
}
.dql-chip:focus-visible {
  outline: var(--nf-focus-ring-width) solid var(--nf-color-focus-ring);
  outline-offset: var(--nf-focus-ring-offset);
}
.dql-chip:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.dql-chip--hero {
  grid-column: 1 / -1;
  padding-block: var(--nf-space-3);
  background: color-mix(in srgb, var(--nf-color-warning-subtle) 55%, var(--nf-color-bg-elevated));
  border-color: color-mix(in srgb, var(--nf-color-warning) 35%, var(--nf-color-border));
  font-weight: var(--nf-weight-medium);
}
.dql-chip--hero:hover:not(:disabled) {
  background: color-mix(in srgb, var(--nf-color-warning-subtle) 80%, var(--nf-color-bg-elevated));
  border-color: var(--nf-color-warning);
}
.dql-chip--project {
  border-style: dashed;
}
.dql-dot {
  flex-shrink: 0;
  /* nf-token-override: component dimension, not a spacing step */
  width: 6px;
  /* nf-token-override: component dimension, not a spacing step */
  height: 6px;
  border-radius: 50%;
  background: var(--nf-color-fg-subtle);
}
.dql-dot--hero {
  /* nf-token-override: component dimension, not a spacing step */
  width: 8px;
  /* nf-token-override: component dimension, not a spacing step */
  height: 8px;
  background: var(--nf-color-warning);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--nf-color-warning) 25%, transparent);
}
.dql-dot--owner { background: var(--nf-color-accent); }
.dql-dot--admin { background: var(--nf-color-info); }
.dql-dot--member { background: var(--nf-color-fg-muted); }
.dql-dot--guest {
  background: transparent;
  box-shadow: inset 0 0 0 1px var(--nf-color-fg-subtle);
}
.dql-dot--lead { background: var(--nf-color-success); }
.dql-dot--viewer {
  background: transparent;
  box-shadow: inset 0 0 0 1px var(--nf-color-info);
}
.dql-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dql-email {
  flex-shrink: 0;
  font-family: var(--nf-font-mono);
  font-size: var(--nf-text-xs);
  color: var(--nf-color-fg-subtle);
}
.dql-caption {
  margin: 0;
  padding-top: var(--nf-space-2);
  border-top: 1px dashed var(--nf-color-border);
  font-family: var(--nf-font-mono);
  font-size: var(--nf-text-xs);
  color: var(--nf-color-fg-subtle);
  text-align: center;
  letter-spacing: 0.02em;
}
/* nf-token-override: component dimension, not a spacing step. nf-breakpoint-override: the width at which the email stops fitting on the narrowest phones; a one-off, not a step of the scale */
@media (max-width: 360px) {
  .dql-email { display: none; }
}
`;

export interface LoginSearch {
  redirect?: string;
}

/**
 * SDK-derived response bodies. Local interfaces previously caused silent
 * shape drift (e.g. missing `step` enum narrowing). All three are
 * generated from the Go schema by `make gen-sdk`.
 */
type LoginResponse = components['schemas']['LoginBody'];
type TotpResponse = components['schemas']['AuthTokens'];

function LoginPage(): ReactElement {
  const { t } = useTranslation('auth');
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const { redirect: redirectTarget } = useSearch({ from: '/login' });
  const caps = useCapabilities();

  const {
    register,
    handleSubmit,
    setValue,
    formState: { errors, isSubmitting },
  } = useZodForm<LoginFormValues>(loginSchema, {
    email: '',
    password: '',
  });

  const [serverError, setServerError] = useState<AuthErrorI18nKey | null>(null);
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  const totpGuard = useSubmitGuard();
  const [showMagicLink, setShowMagicLink] = useState(false);
  const [magicLinkEmail, setMagicLinkEmail] = useState('');
  const [magicLinkSent, setMagicLinkSent] = useState(false);
  const magicLinkGuard = useSubmitGuard();
  const magicLinkEmailRef = useRef<HTMLInputElement>(null);

  /**
   * Rate-limit cooldown driven by the server. Set from the `Retry-After`
   * header on a 429 response. While `> 0` the form inputs and submit
   * button are disabled and a banner counts down toward zero.
   */
  const [retryAfterSeconds, setRetryAfterSeconds] = useState(0);
  const { secondsLeft, active: rateLimited } = useRateLimitCountdown({
    seconds: retryAfterSeconds,
    onExpire: () => {
      setRetryAfterSeconds(0);
      setServerError(null);
    },
  });
  const rateLimitMinutes = Math.floor(secondsLeft / 60);
  const rateLimitSeconds = secondsLeft % 60;

  /**
   * Refs used by the post-submit focus management (F4). The first invalid
   * field receives focus when client-side validation fails; otherwise
   * focus moves to the alert region carrying the server error so screen
   * readers immediately announce the failure.
   */
  const emailRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const alertRef = useRef<HTMLParagraphElement>(null);

  const redirectAfterLogin = useCallback((): void => {
    const target = safeRedirectTarget(redirectTarget);
    if (target) {
      window.location.href = target;
    } else {
      void navigate({ to: '/profile', replace: true });
    }
  }, [redirectTarget, navigate]);

  useEffect(() => {
    if (isAuthenticated) {
      redirectAfterLogin();
    }
  }, [isAuthenticated, redirectAfterLogin]);

  // Once a server-side error is rendered, push focus onto the live region
  // so screen-reader users hear it immediately. The ref is `tabIndex={-1}`
  // so the focus is programmatic-only and does not pollute the tab order.
  useEffect(() => {
    if (serverError) {
      alertRef.current?.focus();
    }
  }, [serverError]);

  const completeSignIn = async (accessToken: string): Promise<void> => {
    authStore.getState().setAccessToken(accessToken);
    let data: unknown;
    try {
      data = await apiRequest((client) => client.GET('/me'), 'Failed to load the signed-in user');
    } catch (err) {
      setServerError(mapAuthThrown(err));
      authStore.getState().clearSession();
      return;
    }
    authStore.getState().setSession(accessToken, userFromMe(data as MeResponse));
    redirectAfterLogin();
  };

  const onSubmit = async (values: LoginFormValues): Promise<void> => {
    if (rateLimited) return;
    setServerError(null);
    try {
      // The cooldown after too many attempts rides in a `Retry-After`
      // header, which no error envelope carries, so this call reads the
      // outcome rather than having the refusal thrown for it.
      const settled = await apiSettle(
        (client) =>
          client.POST('/auth/login', {
            body: { email: values.email, password: values.password },
          }),
        'Sign-in failed',
      );
      if (!settled.ok) {
        setServerError(mapAuthThrown(settled.error));
        if (settled.response?.status === 429) {
          // The auth-api signals the cooldown via the Retry-After header,
          // serialised as an integer number of seconds. Anything <= 0 is
          // treated as "no banner" so the form does not get stuck if the
          // header is absent on a future provider.
          const retry = Number.parseInt(settled.response.headers.get('Retry-After') ?? '', 10);
          if (Number.isFinite(retry) && retry > 0) {
            setRetryAfterSeconds(retry);
          }
        }
        return;
      }
      const login = settled.data as LoginResponse;
      if (login.step === 'totp_required') {
        setChallengeToken(login.challengeToken ?? '');
        return;
      }
      if (!login.accessToken) {
        setServerError('auth:errors.generic');
        return;
      }
      await completeSignIn(login.accessToken);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    }
  };

  const handleTotpSubmit = async (credential: TotpCredential): Promise<void> => {
    setServerError(null);
    if (challengeToken == null) return;
    // Synchronous re-entrancy guard: a fast double Enter / double click
    // bails on the second call before reaching the network layer.
    if (totpGuard.guard()) return;
    try {
      const data = await apiRequest(
        (client) => client.POST('/auth/login/totp', { body: { challengeToken, ...credential } }),
        'Sign-in failed',
      );
      const totp = data as TotpResponse;
      await completeSignIn(totp.accessToken);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    } finally {
      totpGuard.end();
    }
  };

  const handleCancelTotp = (): void => {
    setChallengeToken(null);
    setServerError(null);
  };

  const handleMagicLinkSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!magicLinkEmail.trim()) return;
    // Synchronous re-entrancy guard: a fast double Enter / double click
    // bails on the second call before reaching the network layer.
    if (magicLinkGuard.guard()) return;
    setServerError(null);
    try {
      await apiRequest(
        (client) =>
          client.POST('/auth/magic-link/request', { body: { email: magicLinkEmail.trim() } }),
        'Failed to send the sign-in link',
      );
      setMagicLinkSent(true);
    } catch (err) {
      setServerError(mapAuthThrown(err));
    } finally {
      magicLinkGuard.end();
    }
  };

  const handleOpenMagicLink = (): void => {
    setShowMagicLink(true);
    setServerError(null);
  };

  const handleCloseMagicLink = (): void => {
    setShowMagicLink(false);
    setMagicLinkSent(false);
    setServerError(null);
  };

  // The step on screen follows `step` one exit behind: the leaving step collapses
  // first, then `shown` catches up and the next one opens.
  const step: LoginStep = showMagicLink
    ? magicLinkSent
      ? 'magic-sent'
      : 'magic-request'
    : challengeToken != null
      ? 'totp'
      : 'password';
  const [shown, setShown] = useState<LoginStep>(step);
  const handleStepExited = (): void => {
    setShown(step);
  };

  /**
   * Submit handler that wires F4 (focus management). RHF validates first;
   * if `errors` is non-empty after a submit attempt, focus moves to the
   * first invalid field. When validation passes but the server returns
   * an error, focus moves to the alert region after `serverError` is set
   * — handled by the effect at the top of the component since the alert
   * is conditionally rendered.
   */
  const submitWithFocus = handleSubmit(
    async (values) => {
      await onSubmit(values);
    },
    (formErrors) => {
      if (formErrors.email) {
        emailRef.current?.focus();
      } else if (formErrors.password) {
        passwordRef.current?.focus();
      }
    },
  );

  const handleDevQuickLogin = (email: string): void => {
    setValue('email', email, { shouldValidate: true, shouldDirty: true });
    setValue('password', DEV_SEED_PASSWORD, { shouldValidate: true, shouldDirty: true });
    void submitWithFocus();
  };

  return (
    <AuthCard>
      <AuthStep open={step === 'password' && shown === 'password'} onExited={handleStepExited}>
        <form
          onSubmit={(e) => {
            void submitWithFocus(e);
          }}
          noValidate
          className="aw-stack aw-stack-5"
        >
          <h1 className="aw-page-title">{t('login.title')}</h1>

          {rateLimited ? (
            <output aria-live="polite" data-testid="login-rate-limit-banner" className="aw-warning">
              {t('rate_limit.banner', {
                minutes: rateLimitMinutes,
                seconds: rateLimitSeconds,
              })}
            </output>
          ) : null}

          <FormField
            label={t('login.email')}
            required
            {...(errors.email?.message ? { error: t(errors.email.message) } : {})}
          >
            {(control) => {
              const { ref: rhfRef, ...field } = register('email');
              return (
                <Input
                  {...control}
                  {...field}
                  ref={(el) => {
                    rhfRef(el);
                    emailRef.current = el;
                  }}
                  type="email"
                  autoComplete="username"
                  inputMode="email"
                  spellCheck={false}
                  autoFocus
                  disabled={rateLimited}
                />
              );
            }}
          </FormField>

          <FormField
            label={t('login.password')}
            required
            {...(errors.password?.message ? { error: t(errors.password.message) } : {})}
          >
            {(control) => {
              const { ref: rhfRef, ...field } = register('password');
              return (
                <PasswordInput
                  {...control}
                  {...field}
                  ref={(el) => {
                    rhfRef(el);
                    passwordRef.current = el;
                  }}
                  autoComplete="current-password"
                  disabled={rateLimited}
                />
              );
            }}
          </FormField>

          {serverError ? (
            <p
              ref={alertRef}
              role="alert"
              tabIndex={-1}
              className="aw-error"
              data-testid="login-server-error"
            >
              {t(serverError)}
            </p>
          ) : null}

          <Button
            type="submit"
            variant="primary"
            aria-busy={isSubmitting || undefined}
            disabled={isSubmitting || rateLimited}
          >
            {t('login.submit')}
          </Button>

          <OAuthButtonRow
            mode="login"
            {...(redirectTarget ? { redirect: redirectTarget } : {})}
            onError={setServerError}
          />

          {caps?.magicLink && (
            <>
              <div className="aw-row aw-row-3 aw-muted aw-text-sm">
                <hr className="aw-rule" />
                <span>{t('login.magic_link_divider')}</span>
                <hr className="aw-rule" />
              </div>

              <Button type="button" variant="ghost" onClick={handleOpenMagicLink}>
                {t('login.magic_link_button')}
              </Button>
            </>
          )}

          {caps?.registrationOpen !== false && (
            <p className="aw-flush aw-muted aw-text-sm aw-text-center">
              {t('login.no_account')}{' '}
              <Link to="/signup" className="aw-link">
                {t('login.signup_link')}
              </Link>
            </p>
          )}

          {DEV_MODE && (
            <section className="dql-panel" aria-label={t('login.devQuickLogin.title')}>
              <style>{DEV_PANEL_STYLES}</style>

              <header className="dql-head">
                <span className="dql-tag" aria-hidden="true">
                  {t('login.devQuickLogin.tag')}
                </span>
                <h2 className="dql-title">{t('login.devQuickLogin.title')}</h2>
              </header>

              <div className="dql-tier">
                <div className="dql-tier-label" aria-hidden="true">
                  {t('login.devQuickLogin.tiers.instance')}
                </div>
                <div className="dql-grid">
                  {DEV_TIER_INSTANCE.map((role) => (
                    <button
                      key={role.email}
                      type="button"
                      className="dql-chip dql-chip--hero"
                      onClick={() => {
                        handleDevQuickLogin(role.email);
                      }}
                      disabled={isSubmitting || rateLimited}
                    >
                      <span className={`dql-dot dql-dot--${role.rank}`} aria-hidden="true" />
                      <span className="dql-label">{t(role.labelKey)}</span>
                      <span className="dql-email">{role.email}</span>
                    </button>
                  ))}
                </div>
              </div>

              <div className="dql-tier">
                <div className="dql-tier-label" aria-hidden="true">
                  {t('login.devQuickLogin.tiers.workspace')}
                </div>
                <div className="dql-grid">
                  {DEV_TIER_WORKSPACE.map((role) => (
                    <button
                      key={role.email}
                      type="button"
                      className="dql-chip"
                      onClick={() => {
                        handleDevQuickLogin(role.email);
                      }}
                      disabled={isSubmitting || rateLimited}
                    >
                      <span className={`dql-dot dql-dot--${role.rank}`} aria-hidden="true" />
                      <span className="dql-label">{t(role.labelKey)}</span>
                    </button>
                  ))}
                </div>
              </div>

              <div className="dql-tier">
                <div className="dql-tier-label" aria-hidden="true">
                  {t('login.devQuickLogin.tiers.project')}
                </div>
                <div className="dql-grid">
                  {DEV_TIER_PROJECT.map((role) => (
                    <button
                      key={role.email}
                      type="button"
                      className="dql-chip dql-chip--project"
                      onClick={() => {
                        handleDevQuickLogin(role.email);
                      }}
                      disabled={isSubmitting || rateLimited}
                    >
                      <span className={`dql-dot dql-dot--${role.rank}`} aria-hidden="true" />
                      <span className="dql-label">{t(role.labelKey)}</span>
                    </button>
                  ))}
                </div>
              </div>

              <p className="dql-caption">{t('login.devQuickLogin.caption')}</p>
            </section>
          )}
        </form>
      </AuthStep>
      <AuthStep
        open={step === 'magic-request' && shown === 'magic-request'}
        onExited={handleStepExited}
      >
        <form
          onSubmit={(e) => {
            void handleMagicLinkSubmit(e);
          }}
          noValidate
          className="aw-stack aw-stack-5"
        >
          <div className="aw-stack aw-stack-2">
            <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
            <p className="aw-flush aw-muted aw-text-sm">{t('login.magic_link_instructions')}</p>
          </div>
          <FormField label={t('login.email')} required>
            {(control) => (
              <Input
                {...control}
                ref={magicLinkEmailRef}
                type="email"
                autoComplete="email"
                inputMode="email"
                spellCheck={false}
                value={magicLinkEmail}
                onChange={(e) => {
                  setMagicLinkEmail(e.target.value);
                }}
                autoFocus
              />
            )}
          </FormField>
          {serverError ? (
            <p role="alert" className="aw-error">
              {t(serverError)}
            </p>
          ) : null}
          <div className="aw-stack aw-stack-2">
            <Button
              type="submit"
              variant="primary"
              aria-busy={magicLinkGuard.submitting || undefined}
              disabled={magicLinkGuard.submitting || !magicLinkEmail.trim()}
            >
              {t('login.magic_link_submit')}
            </Button>
            <Button type="button" variant="ghost" onClick={handleCloseMagicLink}>
              {t('login.magic_link_back')}
            </Button>
          </div>
        </form>
      </AuthStep>
      <AuthStep open={step === 'magic-sent' && shown === 'magic-sent'} onExited={handleStepExited}>
        <div className="aw-stack aw-stack-5 aw-step-forward">
          <div className="aw-stack aw-stack-2">
            <h1 className="aw-page-title">{t('login.magic_link_title')}</h1>
            <output className="aw-flush aw-muted aw-text-sm">
              {t('login.magic_link_sent', { email: magicLinkEmail })}
            </output>
          </div>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              handleCloseMagicLink();
              setMagicLinkEmail('');
            }}
          >
            {t('login.magic_link_back')}
          </Button>
        </div>
      </AuthStep>
      <AuthStep open={step === 'totp' && shown === 'totp'} onExited={handleStepExited}>
        <TotpChallengeForm
          error={serverError}
          submitting={totpGuard.submitting}
          onSubmit={(credential) => {
            void handleTotpSubmit(credential);
          }}
          onModeChange={() => {
            setServerError(null);
          }}
          cancel={
            <Button
              type="button"
              variant="ghost"
              onClick={handleCancelTotp}
              disabled={totpGuard.submitting}
            >
              {t('login.totp_cancel')}
            </Button>
          }
        />
      </AuthStep>
    </AuthCard>
  );
}

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): LoginSearch => {
    const redirect = typeof search.redirect === 'string' ? search.redirect : undefined;
    return redirect ? { redirect } : {};
  },
  component: LoginPage,
});
