/**
 * TokenList — table of MCP tokens for a workspace, with revoke action.
 *
 * Suspense-ready: relies on `useMcpTokensQuery` (suspense mode).
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type McpTokenSummary, useMcpTokensQuery, useRevokeMcpToken } from './api';
import TokenCreateDialog from './token-create-dialog';

export interface TokenListProps {
  workspaceId: string;
}

function formatDate(unixSec: number | undefined | null, fallback: string): string {
  if (unixSec === undefined || unixSec === null) return fallback;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(unixSec * 1000));
}

export default function TokenList({ workspaceId }: TokenListProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: tokens } = useMcpTokensQuery(workspaceId);
  const revoke = useRevokeMcpToken(workspaceId);
  const [createOpen, setCreateOpen] = useState(false);

  const handleRevoke = async (token: McpTokenSummary): Promise<void> => {
    if (!window.confirm(t('workspace.mcp_tokens.revoke_confirm'))) return;
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
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: '1rem',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('workspace.mcp_tokens.title')}</h1>
          <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.875rem' }}>
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
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('workspace.mcp_tokens.empty')}</p>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table
            style={{
              inlineSize: '100%',
              borderCollapse: 'collapse',
              fontSize: '0.875rem',
            }}
          >
            <thead>
              <tr style={{ textAlign: 'start', color: 'var(--color-muted)' }}>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.name')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.prefix')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.scopes')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.created_at')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'start' }}>
                  {t('workspace.mcp_tokens.table.last_used_at')}
                </th>
                <th style={{ padding: '0.5rem 0.75rem', textAlign: 'end' }}>
                  {t('workspace.mcp_tokens.table.actions')}
                </th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((token) => (
                <tr key={token.id} style={{ borderBlockStart: '1px solid var(--color-border)' }}>
                  <td style={{ padding: '0.75rem' }}>{token.name}</td>
                  <td style={{ padding: '0.75rem', fontFamily: 'var(--font-mono, monospace)' }}>
                    {token.tokenPrefix}
                  </td>
                  <td style={{ padding: '0.75rem' }}>{(token.scopes ?? []).join(' ') || '—'}</td>
                  <td style={{ padding: '0.75rem' }}>{formatDate(token.createdAt, '—')}</td>
                  <td style={{ padding: '0.75rem' }}>
                    {formatDate(token.lastUsedAt, t('workspace.mcp_tokens.never_used'))}
                  </td>
                  <td style={{ padding: '0.75rem', textAlign: 'end' }}>
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
