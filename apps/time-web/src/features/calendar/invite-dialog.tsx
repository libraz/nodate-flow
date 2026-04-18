import { Check, Copy, X } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceStore } from '../../stores/workspace-store';
import { useCreateInviteMutation, useInvitesQuery, useRevokeInviteMutation } from './api';

interface InviteDialogProps {
  calendarId: string;
  open: boolean;
  onClose: () => void;
}

export default function InviteDialog({
  calendarId,
  open,
  onClose,
}: InviteDialogProps): ReactElement | null {
  const { t, i18n } = useTranslation();
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
  const { data: invites, isLoading } = useInvitesQuery(wsId, calendarId, open);
  const createMutation = useCreateInviteMutation(wsId, calendarId);
  const revokeMutation = useRevokeInviteMutation(wsId, calendarId);

  const [role, setRole] = useState('editor');
  const [copiedToken, setCopiedToken] = useState<string | null>(null);

  const handleCreate = useCallback(() => {
    createMutation.mutate({ role });
  }, [role, createMutation]);

  const handleCopy = useCallback((token: string) => {
    const url = `${window.location.origin}/invites/${token}`;
    void navigator.clipboard.writeText(url).then(() => {
      setCopiedToken(token);
      setTimeout(() => setCopiedToken(null), 2000);
    });
  }, []);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--color-overlay)]">
      <div className="glass-surface-heavy w-full max-w-md rounded-[var(--radius-lg)] p-6 ring-1 ring-[var(--color-border)]">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold" style={{ color: 'var(--color-text-primary)' }}>
            {t('invite.title')}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="hover:opacity-80"
            style={{ color: 'var(--color-text-tertiary)' }}
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="mb-4 flex items-center gap-2">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="flex-1 rounded-md border border-[var(--color-border)] px-3 py-2 text-sm"
            style={{
              backgroundColor: 'var(--color-surface-inset)',
              color: 'var(--color-text-primary)',
            }}
          >
            <option value="manager">{t('members.roleManager')}</option>
            <option value="editor">{t('members.roleEditor')}</option>
            <option value="viewer">{t('members.roleViewer')}</option>
          </select>
          <button
            type="button"
            onClick={handleCreate}
            disabled={createMutation.isPending}
            className="rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-text-on-accent)] hover:opacity-90 disabled:opacity-50"
          >
            {t('invite.createLink')}
          </button>
        </div>

        <div className="max-h-64 space-y-3 overflow-y-auto">
          {isLoading ? (
            <p className="text-sm" style={{ color: 'var(--color-text-secondary)' }}>
              {t('common.loading')}
            </p>
          ) : invites?.length === 0 ? (
            <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
              {t('invite.noLinks')}
            </p>
          ) : (
            invites?.map((invite) => (
              <div
                key={invite.id}
                className="flex items-center justify-between rounded-md border border-[var(--color-border)] px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span
                      className="rounded bg-[var(--color-surface-inset)] px-1.5 py-0.5 text-xs font-medium capitalize"
                      style={{ color: 'var(--color-text-primary)' }}
                    >
                      {invite.role}
                    </span>
                    <span className="text-xs" style={{ color: 'var(--color-text-secondary)' }}>
                      {invite.useCount === 1
                        ? t('invite.use', { count: invite.useCount })
                        : t('invite.uses', { count: invite.useCount })}
                      {invite.maxUses != null ? ` / ${invite.maxUses}` : ''}
                    </span>
                  </div>
                  {invite.expiresAt ? (
                    <p className="mt-0.5 text-xs" style={{ color: 'var(--color-text-tertiary)' }}>
                      {t('invite.expires', {
                        date: DateTime.fromISO(invite.expiresAt)
                          .setLocale(i18n.language)
                          .toLocaleString(DateTime.DATE_MED),
                      })}
                    </p>
                  ) : null}
                </div>
                <div className="ml-2 flex items-center gap-1">
                  <button
                    type="button"
                    onClick={() => handleCopy(invite.token)}
                    className="rounded p-1 hover:bg-[var(--color-hover)]"
                    style={{ color: 'var(--color-text-tertiary)' }}
                    title={t('invite.copyLink')}
                  >
                    {copiedToken === invite.token ? (
                      <Check className="h-4 w-4" style={{ color: 'var(--color-accent)' }} />
                    ) : (
                      <Copy className="h-4 w-4" />
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => revokeMutation.mutate(invite.id)}
                    className="group rounded p-1 hover:bg-[var(--color-hover)]"
                    style={{ color: 'var(--color-text-tertiary)' }}
                    title={t('invite.revoke')}
                  >
                    <X className="h-4 w-4 group-hover:text-[var(--color-danger)]" />
                  </button>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
