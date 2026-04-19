import { Check, Copy, X } from 'lucide-react';
import { DateTime } from 'luxon';
import { type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Select from '@nodate-flow/ui/primitives/select';

import { useWorkspace } from '../../stores/workspace-store';
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
  const wsId = useWorkspace((s) => s.workspaceId) ?? '';
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

  return (
    <Dialog open={open} onClose={onClose} title={t('invite.title')} fullScreenOnMobile>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
        <Select value={role} onChange={(e) => setRole(e.target.value)} style={{ flex: 1 }}>
          <option value="manager">{t('members.role_manager')}</option>
          <option value="editor">{t('members.role_editor')}</option>
          <option value="viewer">{t('members.role_viewer')}</option>
        </Select>
        <Button
          variant="primary"
          size="sm"
          onClick={handleCreate}
          disabled={createMutation.isPending}
        >
          {t('invite.create_link')}
        </Button>
      </div>

      <div
        style={{
          maxBlockSize: '16rem',
          overflowY: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
        }}
      >
        {isLoading ? (
          <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
            {t('common.loading')}
          </p>
        ) : invites?.length === 0 ? (
          <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
            {t('invite.no_links')}
          </p>
        ) : (
          invites?.map((invite) => (
            <div
              key={invite.id}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                borderRadius: 'var(--nf-radius-md)',
                border: '1px solid var(--nf-color-border)',
                padding: 'var(--nf-space-2) var(--nf-space-3)',
              }}
            >
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
                  <Badge tone="neutral">{invite.role}</Badge>
                  <span
                    style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-muted)' }}
                  >
                    {invite.useCount === 1
                      ? t('invite.use', { count: invite.useCount })
                      : t('invite.uses', { count: invite.useCount })}
                    {invite.maxUses != null ? ` / ${invite.maxUses}` : ''}
                  </span>
                </div>
                {invite.expiresAt ? (
                  <p
                    style={{
                      marginBlockStart: 'var(--nf-space-1)',
                      fontSize: 'var(--nf-text-xs)',
                      color: 'var(--nf-color-fg-subtle)',
                    }}
                  >
                    {t('invite.expires', {
                      date: DateTime.fromISO(invite.expiresAt)
                        .setLocale(i18n.language)
                        .toLocaleString(DateTime.DATE_MED),
                    })}
                  </p>
                ) : null}
              </div>
              <div
                style={{
                  marginInlineStart: 'var(--nf-space-2)',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-1)',
                }}
              >
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => handleCopy(invite.token)}
                  aria-label={t('invite.copy_link')}
                >
                  {copiedToken === invite.token ? (
                    <Check size={16} style={{ color: 'var(--nf-color-accent)' }} />
                  ) : (
                    <Copy size={16} />
                  )}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => revokeMutation.mutate(invite.id)}
                  aria-label={t('invite.revoke')}
                >
                  <X size={16} />
                </Button>
              </div>
            </div>
          ))
        )}
      </div>
    </Dialog>
  );
}
