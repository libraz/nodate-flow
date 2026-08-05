/**
 * TokenList — table of MCP tokens for a workspace, with revoke action.
 *
 * Suspense-ready: relies on `useMcpTokensQuery` (suspense mode).
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { formatEpochDateTime } from '../../lib/format';
import { type McpTokenSummary, useMcpTokensQuery, useRevokeMcpToken } from './api';
import TokenCreateDialog from './token-create-dialog';

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
  const locale = i18n.resolvedLanguage ?? 'en';

  const handleRevoke = async (token: McpTokenSummary): Promise<void> => {
    if (!(await confirmAction({ message: t('workspace.mcp_tokens.revoke_confirm') }))) return;
    try {
      await revoke.mutateAsync(token.id);
      toaster.show({ tone: 'success', message: t('workspace.mcp_tokens.revoked') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.mcp_tokens.errors.revoke_failed'),
      });
    }
  };

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: 'var(--nf-space-4)',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>
            {t('workspace.mcp_tokens.title')}
          </h1>
          <p
            style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}
          >
            {t('workspace.mcp_tokens.description')}
          </p>
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('workspace.mcp_tokens.create')}
        </Button>
      </header>

      {tokens.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('workspace.mcp_tokens.empty')}
        </p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table
            style={{
              inlineSize: '100%',
              borderCollapse: 'collapse',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            <thead>
              <tr style={{ textAlign: 'start', color: 'var(--nf-color-fg-muted)' }}>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.name')}
                </th>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.prefix')}
                </th>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.scopes')}
                </th>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.created_at')}
                </th>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.last_used_at')}
                </th>
                <th style={{ padding: 'var(--nf-space-2) var(--nf-space-3)', textAlign: 'end' }}>
                  {t('workspace.mcp_tokens.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((token) => (
                <tr key={token.id} style={{ borderBlockStart: '1px solid var(--nf-color-border)' }}>
                  <td style={{ padding: 'var(--nf-space-3)' }}>{token.name}</td>
                  <td
                    style={{
                      padding: 'var(--nf-space-3)',
                      fontFamily: 'var(--nf-font-mono, monospace)',
                    }}
                  >
                    {token.tokenPrefix}
                  </td>
                  <td style={{ padding: 'var(--nf-space-3)' }}>
                    {(token.scopes ?? []).join(' ') || '—'}
                  </td>
                  <td style={{ padding: 'var(--nf-space-3)' }}>
                    {formatUnixDateTime(token.createdAt, locale, '—')}
                  </td>
                  <td style={{ padding: 'var(--nf-space-3)' }}>
                    {formatUnixDateTime(
                      token.lastUsedAt,
                      locale,
                      t('workspace.mcp_tokens.never_used'),
                    )}
                  </td>
                  <td style={{ padding: 'var(--nf-space-3)', textAlign: 'end' }}>
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={() => {
                        void handleRevoke(token);
                      }}
                    >
                      {t('workspace.mcp_tokens.revoke')}
                    </Button>
                  </td>
                </tr>
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
    </section>
  );
}
