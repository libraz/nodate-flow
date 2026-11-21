/**
 * /security -- Password change, TOTP management, active sessions.
 * Authenticated-only, guarded by the _authenticated layout route.
 */

import type { components } from '@nodate-flow/sdk';
import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import ErrorFallback from '@nodate-flow/ui/primitives/error-fallback';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../../components/page-header';
import SectionCard, { SectionActions } from '../../components/section-card';
import {
  type ChangePasswordFormValues,
  changePasswordSchema,
} from '../../features/auth/auth-schemas';
import { selectUser, useAuth } from '../../features/auth/auth-store';
import PasswordInput, { PasswordField } from '../../features/auth/password-input';
import { TotpSection } from '../../features/auth/totp-section';
import { capsOf, useCapabilities } from '../../features/auth/use-capabilities';
import { apiRequest } from '../../lib/api';
import { type AuthErrorI18nKey, mapAuthThrown, refusalCode } from '../../lib/auth-errors';
import { logError } from '../../lib/log';

export { RecoveryCodesView } from '../../features/auth/totp-section';

/** SDK-derived response bodies. Local interfaces caused silent shape drift. */
type SessionSummary = components['schemas']['SessionSummary'];
type ListSessionsOutputBody = components['schemas']['ListSessionsOutputBody'];
type RevokeAllOtherSessionsOutputBody = components['schemas']['RevokeAllOtherSessionsOutputBody'];

/**
 * @brief Identifier returned by parseUserAgent when a token is recognized.
 * Concrete browser/OS labels remain English-derived strings (e.g. "Chrome 124"),
 * while the unknown tokens are translation keys resolved at render time so
 * non-English locales don't see a hardcoded "Unknown".
 */
type BrowserToken = { kind: 'name'; label: string } | { kind: 'unknown' };
type OsToken = { kind: 'name'; label: string } | { kind: 'unknown' };

interface ParsedUA {
  browser: BrowserToken;
  os: OsToken;
}

/**
 * @brief Extract the numeric version token that follows a product marker.
 * @param ua User-Agent string.
 * @param marker Product marker including trailing slash (e.g. "Chrome/").
 * @return Numeric major version as a string, or empty string when not found.
 *
 * Avoids regex per project convention: locates the marker via indexOf, then
 * consumes leading digits until a non-digit byte.
 */
export function extractVersion(ua: string, marker: string): string {
  const idx = ua.indexOf(marker);
  if (idx === -1) return '';
  let i = idx + marker.length;
  let version = '';
  while (i < ua.length) {
    const ch = ua.charCodeAt(i);
    if (ch < 48 || ch > 57) break; // not 0-9
    version += ua[i];
    i += 1;
  }
  return version;
}

/**
 * @brief Parse a User-Agent string into glanceable browser + OS tokens.
 * @param ua Raw User-Agent header value.
 * @return Tagged tokens; `kind === 'unknown'` lets the caller pick the
 *         appropriate i18n key instead of receiving a hardcoded "Unknown".
 *
 * Hand-rolled, regex-free parser covering common desktop and mobile agents.
 * Order matters for browsers: Edge UA embeds "Chrome/" and Chrome UA embeds
 * "Safari/", so detection runs Edge -> Firefox -> Chrome -> Safari.
 */
export function parseUserAgent(ua: string): ParsedUA {
  let browser: BrowserToken = { kind: 'unknown' };
  const edgeVer = extractVersion(ua, 'Edg/');
  const firefoxVer = extractVersion(ua, 'Firefox/');
  const chromeVer = extractVersion(ua, 'Chrome/');
  const safariVer = extractVersion(ua, 'Version/');
  if (edgeVer) browser = { kind: 'name', label: `Edge ${edgeVer}` };
  else if (firefoxVer) browser = { kind: 'name', label: `Firefox ${firefoxVer}` };
  else if (chromeVer) browser = { kind: 'name', label: `Chrome ${chromeVer}` };
  else if (safariVer && ua.indexOf('Safari/') !== -1)
    browser = { kind: 'name', label: `Safari ${safariVer}` };

  // Order matters: iPad/iPhone UAs include "Mac OS X" / "Macintosh", and
  // Android UAs include "Linux", so the more specific match must come first.
  let os: OsToken = { kind: 'unknown' };
  if (ua.indexOf('Windows NT') !== -1) os = { kind: 'name', label: 'Windows' };
  else if (ua.indexOf('Android') !== -1) os = { kind: 'name', label: 'Android' };
  else if (ua.indexOf('iPhone') !== -1 || ua.indexOf('iPad') !== -1 || ua.indexOf('iOS') !== -1)
    os = { kind: 'name', label: 'iOS' };
  else if (ua.indexOf('Mac OS X') !== -1 || ua.indexOf('Macintosh') !== -1)
    os = { kind: 'name', label: 'macOS' };
  else if (ua.indexOf('Linux') !== -1) os = { kind: 'name', label: 'Linux' };

  return { browser, os };
}

/** Seconds in a day — shared between relative and absolute branches. */
const SECONDS_PER_DAY = 86_400;
/** Window within which we prefer a relative (e.g. "2 hours ago") label. */
const RELATIVE_WINDOW_SECONDS = 7 * SECONDS_PER_DAY;

/**
 * @brief Render a unix-seconds timestamp as a locale-aware string.
 * @param ts Unix seconds.
 * @param locale BCP 47 locale (e.g. "ja" / "en").
 * @return Relative phrase (e.g. "2 時間前") within 7 days, otherwise a
 *         locale-formatted absolute date.
 *
 * Intentionally local to this file: the audit-logs page renders absolute
 * timestamps only, so the two use cases do not share a util yet. Uses
 * `Intl.RelativeTimeFormat` and `Intl.DateTimeFormat` — no date library.
 */
function formatRelative(ts: number, locale: string): string {
  const nowSec = Math.floor(Date.now() / 1000);
  const diffSec = ts - nowSec; // negative = past, positive = future
  const absDiff = Math.abs(diffSec);

  if (absDiff < RELATIVE_WINDOW_SECONDS) {
    try {
      const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
      if (absDiff < 60) {
        return rtf.format(Math.round(diffSec), 'second');
      }
      if (absDiff < 3_600) {
        return rtf.format(Math.round(diffSec / 60), 'minute');
      }
      if (absDiff < SECONDS_PER_DAY) {
        return rtf.format(Math.round(diffSec / 3_600), 'hour');
      }
      return rtf.format(Math.round(diffSec / SECONDS_PER_DAY), 'day');
    } catch {
      // Fall through to absolute format on environments lacking RTF.
    }
  }

  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(ts * 1000));
  } catch {
    return new Date(ts * 1000).toISOString();
  }
}

export function SecurityPage(): ReactElement {
  const { t, i18n } = useTranslation('auth');
  const locale = i18n.resolvedLanguage ?? 'en';
  // Password change and two-factor management depend on the capabilities
  // and on the account holding a local password; they render only once
  // both are known.
  const capsState = useCapabilities();
  const caps = capsOf(capsState);
  const hasPassword = useAuth(selectUser)?.hasPassword === true;
  const passwordLogin = caps?.passwordLogin === true && hasPassword;
  const totpManagement = caps?.totp === true && hasPassword;

  // -- Password change --
  const [passwordError, setPasswordError] = useState<AuthErrorI18nKey | null>(null);
  const [passwordSuccess, setPasswordSuccess] = useState(false);

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useZodForm<ChangePasswordFormValues>(changePasswordSchema, {
    currentPassword: '',
    newPassword: '',
  });

  const onChangePassword = async (values: ChangePasswordFormValues): Promise<void> => {
    setPasswordError(null);
    setPasswordSuccess(false);
    try {
      await apiRequest(
        (client) =>
          client.POST('/me/password', {
            body: {
              currentPassword: values.currentPassword,
              newPassword: values.newPassword,
            },
          }),
        'Failed to change the password',
      );
      setPasswordSuccess(true);
      reset();
    } catch (err) {
      setPasswordError(mapAuthThrown(err));
    }
  };

  // -- Active sessions --
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const sessionPresence = usePresenceList(sessions, (session) => session.id);
  const stagger = useFirstLoadStagger();
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [revokingAll, setRevokingAll] = useState(false);
  const [sessionsReload, setSessionsReload] = useState(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: sessionsReload re-runs the fetch
  useEffect(() => {
    let cancelled = false;
    void apiRequest((client) => client.GET('/me/sessions'), 'Failed to load sessions')
      .then((res) => {
        if (!cancelled) {
          const body = res as ListSessionsOutputBody | undefined;
          setSessions(body?.items ?? []);
          setSessionsLoading(false);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          logError('Failed to fetch sessions', err);
          setSessionsLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [sessionsReload]);

  const hasOtherSessions = sessions.some((s) => !s.current);

  const handleRevokeAllOthers = async (): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('security.session_revoke_all_confirm'),
      confirmLabel: t('security.session_revoke_all'),
    });
    if (!confirmed) return;
    setRevokingAll(true);
    try {
      const res = await apiRequest(
        (client) => client.DELETE('/me/sessions'),
        'Failed to revoke the other sessions',
      );
      const revoked = (res as RevokeAllOtherSessionsOutputBody | undefined)?.revoked ?? 0;
      toaster.show({
        tone: 'success',
        message: t('security.session_revoked_all', { count: revoked }),
      });
      setSessionsReload((n) => n + 1);
    } catch (err) {
      const code = refusalCode(err);
      toaster.show({
        message: code
          ? `${t('security.session_revoke_all_failed')} (${code})`
          : t('security.session_revoke_all_failed'),
        tone: 'danger',
      });
    } finally {
      setRevokingAll(false);
    }
  };

  const handleRevokeSession = async (sessionId: string): Promise<void> => {
    setRevokingId(sessionId);
    try {
      await apiRequest(
        (client) => client.DELETE('/me/sessions/{sessionId}', { params: { path: { sessionId } } }),
        'Failed to revoke the session',
      );
      setSessions((prev) => prev.filter((s) => s.id !== sessionId));
    } catch (err) {
      const code = refusalCode(err);
      toaster.show({
        message: code
          ? `${t('security.session_revoke_failed')} (${code})`
          : t('security.session_revoke_failed'),
        tone: 'danger',
      });
    } finally {
      setRevokingId(null);
    }
  };

  /**
   * Build the localized "Browser · OS" label without leaking English
   * "Unknown" into JA/ZH locales.
   */
  const formatDevice = (ua: string): string => {
    const { browser, os } = parseUserAgent(ua);
    const browserLabel =
      browser.kind === 'name' ? browser.label : t('security.session.browser_unknown');
    const osLabel = os.kind === 'name' ? os.label : t('security.session.os_unknown');
    return `${browserLabel} · ${osLabel}`;
  };

  return (
    <>
      <PageHeader title={t('security.title')} description={t('security.description')} />
      <div className="aw-stack aw-stack-6">
        {capsState.status === 'error' && (
          <ErrorFallback
            title={t('fatal.title')}
            action={{ label: t('fatal.retry'), onClick: capsState.retry }}
          />
        )}

        {passwordLogin && (
          <SectionCard
            title={t('security.password_title')}
            description={t('security.sections.password_description')}
          >
            <form
              onSubmit={(e) => {
                void handleSubmit(onChangePassword)(e);
              }}
              noValidate
              className="aw-stack aw-stack-4"
            >
              <PasswordField
                label={t('security.current_password')}
                required
                {...(errors.currentPassword?.message
                  ? { error: t(errors.currentPassword.message) }
                  : {})}
              >
                {(control) => {
                  const { ref, ...field } = register('currentPassword');
                  return (
                    <PasswordInput
                      {...control}
                      {...field}
                      ref={ref}
                      autoComplete="current-password"
                    />
                  );
                }}
              </PasswordField>

              <PasswordField
                label={t('security.new_password')}
                required
                {...(errors.newPassword?.message ? { error: t(errors.newPassword.message) } : {})}
              >
                {(control) => {
                  const { ref, ...field } = register('newPassword');
                  return (
                    <PasswordInput {...control} {...field} ref={ref} autoComplete="new-password" />
                  );
                }}
              </PasswordField>

              <SectionActions
                status={
                  passwordError ? (
                    <p role="alert" className="aw-error">
                      {t(passwordError)}
                    </p>
                  ) : passwordSuccess ? (
                    <output className="aw-success">{t('security.password_changed')}</output>
                  ) : null
                }
              >
                <Button
                  type="submit"
                  variant="primary"
                  aria-busy={isSubmitting || undefined}
                  disabled={isSubmitting}
                >
                  {t('security.password_change')}
                </Button>
              </SectionActions>
            </form>
          </SectionCard>
        )}

        {totpManagement && (
          <SectionCard title={t('security.totp.title')}>
            <TotpSection />
          </SectionCard>
        )}

        <SectionCard
          title={t('security.sessions_title')}
          description={t('security.sections.sessions_description')}
        >
          {sessionsLoading ? (
            <div className="aw-stack aw-stack-3" aria-busy="true">
              <Skeleton />
              <Skeleton />
            </div>
          ) : sessions.length === 0 ? (
            <p className="aw-flush aw-muted aw-text-sm">{t('security.sessions_empty')}</p>
          ) : (
            <ul className="aw-list">
              {sessionPresence.entries.map(({ key, item: session, present }, index) => (
                <PresenceItem
                  key={key}
                  as="li"
                  present={present}
                  onExited={() => {
                    sessionPresence.onExited(key);
                  }}
                  className={`aw-presence-item ${motion.enter}`}
                  style={stagger(index)}
                >
                  <div
                    className={`aw-row-between aw-surface ${session.current ? 'aw-surface-elevated' : ''}`}
                  >
                    <div>
                      <p
                        className={`aw-flush aw-text-sm ${session.current ? 'aw-weight-semibold' : ''}`}
                        title={session.userAgent || undefined}
                      >
                        {session.userAgent
                          ? formatDevice(session.userAgent)
                          : t('security.session_unknown_agent')}
                        {session.current ? ` (${t('security.session_current')})` : ''}
                      </p>
                      <p className="aw-flush aw-muted aw-text-xs">{session.ipAddress}</p>
                      <p className="aw-flush aw-muted aw-text-xs">
                        {t('security.session_created')}: {formatRelative(session.createdAt, locale)}
                        {' · '}
                        {t('security.session_last_active')}:{' '}
                        {session.lastUsedAt !== undefined && session.lastUsedAt !== null
                          ? formatRelative(session.lastUsedAt, locale)
                          : t('security.session_last_active_unknown')}
                      </p>
                    </div>
                    {!session.current ? (
                      <Button
                        type="button"
                        variant="ghost"
                        aria-busy={revokingId === session.id || undefined}
                        disabled={revokingId === session.id}
                        onClick={() => {
                          void handleRevokeSession(session.id);
                        }}
                      >
                        {t('security.session_revoke')}
                      </Button>
                    ) : null}
                  </div>
                </PresenceItem>
              ))}
            </ul>
          )}
          {hasOtherSessions ? (
            <div>
              <Button
                type="button"
                variant="danger"
                aria-busy={revokingAll || undefined}
                disabled={revokingAll}
                onClick={() => {
                  void handleRevokeAllOthers();
                }}
              >
                {t('security.session_revoke_all')}
              </Button>
            </div>
          ) : null}
        </SectionCard>
      </div>
    </>
  );
}

export const Route = createFileRoute('/_authenticated/security')({
  component: SecurityPage,
});
