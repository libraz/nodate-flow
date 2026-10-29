/**
 * TokenList — table of MCP tokens for a workspace, with revoke action.
 *
 * Suspense-ready: relies on `useMcpTokensQuery` (suspense mode).
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import { cx } from '@nodate-flow/ui/lib/cx';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceRow from '@nodate-flow/ui/primitives/presence-row';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import { type McpTokenSummary, useMcpTokensQuery, useRevokeMcpToken } from './api';
import TokenCreateDialog from './token-create-dialog';
import styles from './token-list.module.css';

export interface TokenListProps {
  workspaceId: string;
}

/** Format a unix-seconds timestamp, returning `fallback` when absent. */
function formatUnixDateTime(
  unixSec: number | undefined | null,
  locale: string,
  fallback: string,
): string {
  return formatEpochDateTime(unixSec, locale) ?? fallback;
}

export default function TokenList({ workspaceId }: TokenListProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { data: tokens } = useMcpTokensQuery(workspaceId);
  const revoke = useRevokeMcpToken(workspaceId);
  const [createOpen, setCreateOpen] = useState(false);
  // Tokens present at first paint; a token created afterwards fades in.
  const [initialIds] = useState<ReadonlySet<string>>(() => new Set(tokens.map((tk) => tk.id)));
  const { entries, onExited } = usePresenceList(tokens, (token) => token.id);
  const revokingId = revoke.isPending ? revoke.variables : undefined;
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleRevoke = async (token: McpTokenSummary): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('workspace.mcp_tokens.revoke_confirm'),
      confirmLabel: t('workspace.mcp_tokens.revoke'),
    });
    if (!confirmed) return;
    try {
      await revoke.mutateAsync(token.id);
      toaster.show({ tone: 'success', message: t('workspace.mcp_tokens.revoked') });
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.mcp_tokens.errors.revoke_failed'),
      });
    }
  };

  const openCreate = (): void => {
    setCreateOpen(true);
  };

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('workspace.mcp_tokens.title')}</h1>
          <p className={ui.lede}>{t('workspace.mcp_tokens.description')}</p>
        </div>
        <Button type="button" variant="primary" onClick={openCreate}>
          {t('workspace.mcp_tokens.create')}
        </Button>
      </header>

      {tokens.length === 0 ? (
        <EmptyState
          title={t('workspace.mcp_tokens.empty')}
          className={ui.empty}
          action={
            <Button type="button" onClick={openCreate}>
              {t('workspace.mcp_tokens.create')}
            </Button>
          }
        />
      ) : (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th>{t('workspace.mcp_tokens.table.name')}</th>
                <th>{t('workspace.mcp_tokens.table.prefix')}</th>
                <th>{t('workspace.mcp_tokens.table.scopes')}</th>
                <th>{t('workspace.mcp_tokens.table.created_at')}</th>
                <th>{t('workspace.mcp_tokens.table.last_used_at')}</th>
                <th className={ui.alignEnd}>{t('workspace.mcp_tokens.table.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(({ key, item: token, present }) => (
                <PresenceRow
                  key={key}
                  present={present}
                  arrived={!initialIds.has(token.id)}
                  onExited={() => {
                    onExited(key);
                  }}
                >
                  <td>
                    <TruncatedText text={token.name} className={styles.nameCell} />
                  </td>
                  <td className={ui.mono}>{token.tokenPrefix}</td>
                  <td>
                    {(token.scopes ?? []).length === 0 ? (
                      <span className={ui.muted}>—</span>
                    ) : (
                      <span className={styles.scopes}>
                        {(token.scopes ?? []).map((scope) => (
                          <code key={scope} className={styles.scope}>
                            {scope}
                          </code>
                        ))}
                      </span>
                    )}
                  </td>
                  <td className={ui.numeric}>{formatUnixDateTime(token.createdAt, locale, '—')}</td>
                  <td className={cx(ui.numeric, token.lastUsedAt == null && ui.muted)}>
                    {formatUnixDateTime(
                      token.lastUsedAt,
                      locale,
                      t('workspace.mcp_tokens.never_used'),
                    )}
                  </td>
                  <td className={ui.alignEnd}>
                    <Button
                      type="button"
                      size="sm"
                      disabled={revoke.isPending}
                      aria-busy={revokingId === token.id}
                      onClick={() => {
                        void handleRevoke(token);
                      }}
                    >
                      {t('workspace.mcp_tokens.revoke')}
                    </Button>
                  </td>
                </PresenceRow>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <TokenCreateDialog
        workspaceId={workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />
    </div>
  );
}
