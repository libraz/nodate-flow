/**
 * /admin/signin-allowlist -- Instance OAuth sign-in allowlist with
 * add / withdraw / restore.
 *
 * What this list decides is who may sign in to the whole deployment, and
 * two of its rules read backwards from a plain table, so the screen says
 * both out loud:
 *
 *   - an empty list restricts nobody. Every other admin table means "none
 *     of these exist yet"; this one means "everyone is admitted", and an
 *     administrator who reads it the usual way locks the wrong conclusion
 *     in.
 *   - the environment carries a half of the allowlist that this screen
 *     cannot see or remove. Sign-in admits the union of both, so an empty
 *     table is not proof that sign-in is open.
 *
 * A withdrawn entry keeps its claim on its (kind, value) pair, so it stays
 * listed — in its own section, below the live ones — and adding it again
 * revives it.
 */

import type { components } from '@nodate-flow/sdk';
import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl, {
  type SegmentedControlOption,
} from '@nodate-flow/ui/primitives/segmented-control';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { apiRequest } from '../../../lib/api';
import { refusalCode } from '../../../lib/auth-errors';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { useSubmitGuard } from '../../../lib/use-submit-guard';
import styles from './signin-allowlist.module.css';

/**
 * SDK-derived shapes, so a server-side field rename surfaces as a
 * typecheck failure instead of a column that silently renders empty.
 */
type AllowlistEntry = components['schemas']['OAuthSignInAllowlistEntry'];
type AllowlistResponse = components['schemas']['ListOAuthSignInAllowlistOutputBody'];
type EntryKind = AllowlistEntry['kind'];

/**
 * One request covers the list. The endpoint caps `limit` at 200, and an
 * allowlist that reaches it says so on screen rather than quietly showing
 * a prefix of itself.
 */
const PAGE_LIMIT = 200;

/** The list as it stands on the server, or null when the read failed. */
async function fetchAllowlist(): Promise<{ items: AllowlistEntry[]; total: number } | null> {
  const result = await apiRequest(
    (client) =>
      client.GET('/admin/oauth-signin-allowlist', {
        params: { query: { limit: PAGE_LIMIT, offset: 0 } },
      }),
    'Failed to load the sign-in allowlist',
    { onError: 'empty', empty: null },
  );
  if (result === null) return null;
  const body = result as AllowlistResponse;
  return { items: body.items ?? [], total: body.total };
}

export function SignInAllowlistPage(): ReactElement {
  const { t, i18n } = useTranslation('admin');
  const locale = i18n.resolvedLanguage ?? 'en';
  const [entries, setEntries] = useState<AllowlistEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [addError, setAddError] = useState<string | null>(null);
  const [kind, setKind] = useState<EntryKind>('domain');
  const [value, setValue] = useState('');
  const [notes, setNotes] = useState('');
  const addGuard = useSubmitGuard();
  const rowGuard = useSubmitGuard();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    void fetchAllowlist().then((result) => {
      if (cancelled) return;
      if (result === null) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setEntries(result.items);
      setTotal(result.total);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [t]);

  /**
   * Re-reads the list after a write. The server normalizes a value and
   * revives a withdrawn row in place, so what an entry looks like
   * afterwards is only knowable by asking.
   */
  const refresh = async (): Promise<void> => {
    const result = await fetchAllowlist();
    if (result === null) {
      setError(t('errors.generic'));
      return;
    }
    setError(null);
    setEntries(result.items);
    setTotal(result.total);
  };

  const handleAdd = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setAddError(null);
    const trimmed = value.trim();
    if (trimmed === '') return;
    if (addGuard.guard()) return;
    try {
      try {
        await apiRequest(
          (client) =>
            client.POST('/admin/oauth-signin-allowlist', {
              body: { kind, value: trimmed, ...(notes.trim() ? { notes: notes.trim() } : {}) },
            }),
          'Failed to add the allowlist entry',
        );
      } catch (err) {
        const code = refusalCode(err);
        setAddError(
          code
            ? t('signin_allowlist.errors.add_failed_with_code', { code })
            : t('signin_allowlist.errors.add_failed'),
        );
        return;
      }
      await refresh();
      setValue('');
      setNotes('');
    } finally {
      addGuard.end();
    }
  };

  const handleWithdraw = async (entry: AllowlistEntry): Promise<void> => {
    const ok = await confirmAction({
      tone: 'danger',
      message: t('signin_allowlist.confirm_withdraw', { value: entry.value }),
      confirmLabel: t('signin_allowlist.withdraw'),
    });
    if (!ok) return;
    if (rowGuard.guard()) return;
    try {
      try {
        await apiRequest(
          (client) =>
            client.DELETE('/admin/oauth-signin-allowlist/{entryId}', {
              params: { path: { entryId: entry.id } },
            }),
          'Failed to withdraw the allowlist entry',
        );
      } catch (err) {
        const code = refusalCode(err);
        toaster.show({
          tone: 'danger',
          message: code
            ? t('signin_allowlist.errors.withdraw_failed_with_code', { code })
            : t('signin_allowlist.errors.withdraw_failed'),
        });
        return;
      }
      await refresh();
    } finally {
      rowGuard.end();
    }
  };

  /**
   * Restoring is the add call again: the endpoint is an upsert keyed on
   * the (kind, value) pair, so re-sending the withdrawn entry's own values
   * brings the same row back rather than colliding with it.
   */
  const handleRestore = async (entry: AllowlistEntry): Promise<void> => {
    if (rowGuard.guard()) return;
    try {
      try {
        await apiRequest(
          (client) =>
            client.POST('/admin/oauth-signin-allowlist', {
              body: {
                kind: entry.kind,
                value: entry.value,
                ...(entry.notes ? { notes: entry.notes } : {}),
              },
            }),
          'Failed to restore the allowlist entry',
        );
      } catch (err) {
        const code = refusalCode(err);
        toaster.show({
          tone: 'danger',
          message: code
            ? t('signin_allowlist.errors.restore_failed_with_code', { code })
            : t('signin_allowlist.errors.restore_failed'),
        });
        return;
      }
      await refresh();
    } finally {
      rowGuard.end();
    }
  };

  const active = entries.filter((entry) => entry.enabled);
  const withdrawn = entries.filter((entry) => !entry.enabled);

  /*
   * Both kinds stay visible: picking one decides whether the field beside it
   * takes a whole address or a bare domain, and it relabels that field. A
   * choice that reshapes the rest of the form should not sit behind a menu.
   */
  const kindOptions: SegmentedControlOption<EntryKind>[] = [
    { value: 'domain', label: t('signin_allowlist.kind_domain') },
    { value: 'email', label: t('signin_allowlist.kind_email') },
  ];

  const renderKind = (entryKind: EntryKind): ReactElement => (
    <span
      className={`aw-badge ${entryKind === 'domain' ? styles.kindDomain : styles.kindEmail}`}
      data-kind={entryKind}
    >
      {entryKind === 'domain'
        ? t('signin_allowlist.kind_domain')
        : t('signin_allowlist.kind_email')}
    </span>
  );

  const renderValue = (entry: AllowlistEntry): string =>
    entry.kind === 'domain'
      ? t('signin_allowlist.domain_value', { value: entry.value })
      : entry.value;

  const renderRows = (rows: AllowlistEntry[], withdrawnRows: boolean): ReactElement[] =>
    rows.map((entry) => (
      <tr key={entry.id} className={withdrawnRows ? styles.withdrawnRow : undefined}>
        <td className="aw-td">{renderKind(entry.kind)}</td>
        <td className="aw-td">
          <span className={styles.value}>{renderValue(entry)}</span>
          {withdrawnRows ? (
            <>
              {' '}
              <span className={`aw-badge ${styles.statusWithdrawn}`}>
                {t('signin_allowlist.status_withdrawn')}
              </span>
            </>
          ) : null}
        </td>
        <td className="aw-td">{entry.notes ?? ''}</td>
        <td className="aw-td">
          {entry.addedByDisplayName ?? t('signin_allowlist.added_by_unknown')}
        </td>
        <td className="aw-td">{formatTimestamp(entry.createdAt, { locale })}</td>
        <td className="aw-td">
          {withdrawnRows ? (
            <Button
              variant="default"
              disabled={rowGuard.submitting}
              onClick={() => void handleRestore(entry)}
            >
              {t('signin_allowlist.restore')}
            </Button>
          ) : (
            <Button
              variant="danger"
              disabled={rowGuard.submitting}
              onClick={() => void handleWithdraw(entry)}
            >
              {t('signin_allowlist.withdraw')}
            </Button>
          )}
        </td>
      </tr>
    ));

  const renderTable = (rows: AllowlistEntry[], withdrawnRows: boolean): ReactElement => (
    <div className="aw-table-scroll">
      <table className="aw-table">
        <thead>
          <tr>
            <th className="aw-th">{t('signin_allowlist.kind')}</th>
            <th className="aw-th">{t('signin_allowlist.value')}</th>
            <th className="aw-th">{t('signin_allowlist.notes')}</th>
            <th className="aw-th">{t('signin_allowlist.added_by')}</th>
            <th className="aw-th">{t('signin_allowlist.added_at')}</th>
            <th className="aw-th">
              <VisuallyHidden>{t('signin_allowlist.actions')}</VisuallyHidden>
            </th>
          </tr>
        </thead>
        <tbody>{renderRows(rows, withdrawnRows)}</tbody>
      </table>
    </div>
  );

  return (
    <div className="aw-stack aw-stack-6">
      <header className="aw-stack aw-stack-2">
        <h1 className="aw-page-title">{t('signin_allowlist.title')}</h1>
        <p className="aw-flush aw-muted aw-text-sm">{t('signin_allowlist.description')}</p>
      </header>

      <aside className={styles.notice}>
        <p className={styles.noticeTitle}>{t('signin_allowlist.env_notice_title')}</p>
        <p className={styles.noticeBody}>{t('signin_allowlist.env_notice')}</p>
      </aside>

      {error ? (
        <p role="alert" className="aw-error">
          {error}
        </p>
      ) : null}

      {loading ? (
        <p className="aw-muted">{t('common.loading')}</p>
      ) : (
        <>
          <section className={styles.section} aria-labelledby="allowlist-active-heading">
            <div className={styles.sectionHead}>
              <h2 id="allowlist-active-heading" className="aw-section-title">
                {t('signin_allowlist.active_title')}
              </h2>
              <p className="aw-flush aw-muted aw-text-xs">{t('signin_allowlist.kind_legend')}</p>
            </div>
            {active.length === 0 ? (
              <aside className={`${styles.notice} ${styles.noticeOpen}`}>
                <p className={styles.noticeTitle}>{t('signin_allowlist.open_title')}</p>
                <p className={styles.noticeBody}>{t('signin_allowlist.open_body')}</p>
              </aside>
            ) : (
              renderTable(active, false)
            )}
          </section>

          {withdrawn.length > 0 ? (
            <section className={styles.section} aria-labelledby="allowlist-withdrawn-heading">
              <div className={styles.sectionHead}>
                <h2 id="allowlist-withdrawn-heading" className="aw-section-title">
                  {t('signin_allowlist.withdrawn_title')}
                </h2>
                <p className="aw-flush aw-muted aw-text-xs">
                  {t('signin_allowlist.withdrawn_description')}
                </p>
              </div>
              {renderTable(withdrawn, true)}
            </section>
          ) : null}

          {total > entries.length ? (
            <p className="aw-flush aw-muted aw-text-sm">
              {t('signin_allowlist.truncated', { shown: entries.length, total })}
            </p>
          ) : null}
        </>
      )}

      <section className={styles.panel} aria-labelledby="allowlist-add-heading">
        <h2 id="allowlist-add-heading" className="aw-section-title">
          {t('signin_allowlist.add_title')}
        </h2>
        <form className={styles.form} onSubmit={(e) => void handleAdd(e)}>
          <div className={styles.field}>
            {/*
             * Not a `<label htmlFor>`: the control below is a radiogroup, and
             * a radiogroup is not a labelable element. The group carries the
             * same string as its own accessible name instead.
             */}
            <span className={styles.label} aria-hidden="true">
              {t('signin_allowlist.add_kind_label')}
            </span>
            <SegmentedControl<EntryKind>
              size="sm"
              value={kind}
              onChange={setKind}
              options={kindOptions}
              ariaLabel={t('signin_allowlist.add_kind_label')}
            />
          </div>
          <div className={`${styles.field} ${styles.fieldGrow}`}>
            {/*
             * The label names the value, not the kind. A field called
             * "Domain" would carry the accessible name of the segment that
             * selected it -- the segments name themselves the same way --
             * leaving two controls a screen reader cannot tell apart, one of
             * which decides what the other means.
             */}
            <label className={styles.label} htmlFor="allowlist-value">
              {kind === 'domain'
                ? t('signin_allowlist.add_domain_label')
                : t('signin_allowlist.add_email_label')}
            </label>
            <Input
              id="allowlist-value"
              autoComplete="off"
              spellCheck={false}
              dir="ltr"
              value={value}
              placeholder={
                kind === 'domain'
                  ? t('signin_allowlist.add_domain_placeholder')
                  : t('signin_allowlist.add_email_placeholder')
              }
              onChange={(e) => setValue(e.target.value)}
            />
          </div>
          <div className={`${styles.field} ${styles.fieldGrow}`}>
            <label className={styles.label} htmlFor="allowlist-notes">
              {t('signin_allowlist.add_notes_label')}
            </label>
            <Input
              id="allowlist-notes"
              autoComplete="off"
              value={notes}
              placeholder={t('signin_allowlist.add_notes_placeholder')}
              onChange={(e) => setNotes(e.target.value)}
            />
          </div>
          <Button
            type="submit"
            variant="primary"
            disabled={addGuard.submitting || value.trim() === ''}
          >
            {t('signin_allowlist.add_submit')}
          </Button>
        </form>
        <p className="aw-flush aw-muted aw-text-xs">{t('signin_allowlist.add_hint')}</p>
        {addError ? (
          <p role="alert" className="aw-error">
            {addError}
          </p>
        ) : null}
      </section>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/signin-allowlist')({
  component: SignInAllowlistPage,
});
