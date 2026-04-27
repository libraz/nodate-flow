/**
 * SessionsPanel — /settings/security sessions list. Shows every active
 * refresh-token session for the current user with a Revoke action per
 * row, plus a "Sign out of all other devices" button. Parent must wrap
 * this in <Suspense> (Suspense-mode query).
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { TFunction } from 'i18next';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import {
  type SessionSummary,
  useMySessionsQuery,
  useRevokeAllOtherSessions,
  useRevokeSession,
} from './api';
import styles from './sessions-panel.module.css';

type BrowserId = 'edge' | 'firefox' | 'chrome' | 'safari' | 'unknown';
type OsId = 'windows' | 'macos' | 'android' | 'ios' | 'linux' | 'unknown';

interface ParsedUA {
  browser: BrowserId;
  browserVersion: string;
  os: OsId;
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
function extractVersion(ua: string, marker: string): string {
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
 * @brief Parse a User-Agent string into browser + OS identifiers for i18n.
 * @param ua Raw User-Agent header value.
 * @return Browser and OS identifiers; the caller translates them at render
 *         time. Order matters for browsers: Edge UA embeds "Chrome/" and
 *         Chrome UA embeds "Safari/", so detection runs Edge -> Firefox ->
 *         Chrome -> Safari.
 */
function parseUserAgent(ua: string): ParsedUA {
  let browser: BrowserId = 'unknown';
  let browserVersion = '';
  const edgeVer = extractVersion(ua, 'Edg/');
  const firefoxVer = extractVersion(ua, 'Firefox/');
  const chromeVer = extractVersion(ua, 'Chrome/');
  const safariVer = extractVersion(ua, 'Version/');
  if (edgeVer) {
    browser = 'edge';
    browserVersion = edgeVer;
  } else if (firefoxVer) {
    browser = 'firefox';
    browserVersion = firefoxVer;
  } else if (chromeVer) {
    browser = 'chrome';
    browserVersion = chromeVer;
  } else if (safariVer && ua.indexOf('Safari/') !== -1) {
    browser = 'safari';
    browserVersion = safariVer;
  }

  let os: OsId = 'unknown';
  if (ua.indexOf('Windows NT') !== -1) os = 'windows';
  else if (ua.indexOf('Mac OS X') !== -1 || ua.indexOf('Macintosh') !== -1) os = 'macos';
  else if (ua.indexOf('Android') !== -1) os = 'android';
  else if (ua.indexOf('iPhone') !== -1 || ua.indexOf('iPad') !== -1 || ua.indexOf('iOS') !== -1)
    os = 'ios';
  else if (ua.indexOf('Linux') !== -1) os = 'linux';

  return { browser, browserVersion, os };
}

const BROWSER_KEY: Record<BrowserId, string> = {
  edge: 'browser.edge',
  firefox: 'browser.firefox',
  chrome: 'browser.chrome',
  safari: 'browser.safari',
  unknown: 'browser.unknown',
};

const OS_KEY: Record<OsId, string> = {
  windows: 'os.windows',
  macos: 'os.macos',
  android: 'os.android',
  ios: 'os.ios',
  linux: 'os.linux',
  unknown: 'os.unknown',
};

/**
 * @brief Format a User-Agent string for display in the sessions list.
 * @param ua Raw User-Agent header value.
 * @param tSystem Translator bound to the {@code system-info} namespace.
 * @return "Browser · OS" style label (middle dot separator).
 */
function formatDevice(ua: string, tSystem: TFunction<'system-info'>): string {
  const { browser, browserVersion, os } = parseUserAgent(ua);
  const browserLabel =
    browser === 'unknown'
      ? tSystem(BROWSER_KEY.unknown)
      : tSystem(BROWSER_KEY[browser], { version: browserVersion });
  const osLabel = tSystem(OS_KEY[os]);
  return `${browserLabel} · ${osLabel}`;
}

export { parseUserAgent };

function formatUnix(seconds: number | null | undefined, locale: string): string {
  if (!seconds) return '';
  return new Date(seconds * 1000).toLocaleString(locale);
}

export default function SessionsPanel(): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { t: tSystem } = useTranslation('system-info');
  const { data: sessions } = useMySessionsQuery();
  const revokeOne = useRevokeSession();
  const revokeAll = useRevokeAllOtherSessions();

  const hasOthers = sessions.some((s) => !s.current);

  const handleRevoke = async (id: string): Promise<void> => {
    try {
      await revokeOne.mutateAsync(id);
      toaster.show({ tone: 'success', message: t('security.sessions.revoked') });
    } catch {
      toaster.show({ tone: 'danger', message: t('security.sessions.errors.revoke_failed') });
    }
  };

  const handleRevokeAll = async (): Promise<void> => {
    if (!(await confirmAction({ message: t('security.sessions.revoke_all_confirm') }))) return;
    try {
      const { revoked } = await revokeAll.mutateAsync();
      toaster.show({
        tone: 'success',
        message: t('security.sessions.revoked_all', { count: revoked }),
      });
    } catch {
      toaster.show({ tone: 'danger', message: t('security.sessions.errors.revoke_all_failed') });
    }
  };

  return (
    <section className={styles.section}>
      <p className={styles.description}>{t('security.sessions.description')}</p>

      {sessions.length === 0 ? (
        <p className={styles.empty}>{t('security.sessions.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {sessions.map((s: SessionSummary) => (
            <li key={s.id} className={`${styles.row} ${s.current ? styles.rowCurrent : ''}`.trim()}>
              <div className={styles.identity}>
                <span className={styles.deviceLine} title={s.userAgent || undefined}>
                  {s.userAgent
                    ? formatDevice(s.userAgent, tSystem)
                    : t('security.sessions.unknown_device')}
                  {s.current && (
                    <span className={styles.currentBadge}>
                      {t('security.sessions.current_badge')}
                    </span>
                  )}
                </span>
                <span className={styles.metaPrimary}>
                  {s.ipAddress || t('security.sessions.unknown_ip')}
                </span>
                <span className={styles.metaSecondary}>
                  {t('security.sessions.created_at', {
                    time: formatUnix(s.createdAt, i18n.language),
                  })}
                  {s.lastUsedAt != null && (
                    <>
                      {' · '}
                      {t('security.sessions.last_used_at', {
                        time: formatUnix(s.lastUsedAt, i18n.language),
                      })}
                    </>
                  )}
                </span>
              </div>
              <Button
                type="button"
                variant="danger"
                disabled={s.current || revokeOne.isPending}
                onClick={() => {
                  void handleRevoke(s.id);
                }}
              >
                {t('security.sessions.revoke')}
              </Button>
            </li>
          ))}
        </ul>
      )}

      {hasOthers && (
        <div className={styles.actions}>
          <Button
            type="button"
            variant="danger"
            disabled={revokeAll.isPending}
            onClick={() => {
              void handleRevokeAll();
            }}
          >
            {t('security.sessions.revoke_all')}
          </Button>
        </div>
      )}
    </section>
  );
}
