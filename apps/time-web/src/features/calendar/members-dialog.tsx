import { X } from 'lucide-react';
import { type FormEvent, type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';

import { useWorkspace } from '../../stores/workspace-store';
import {
  useAddMemberMutation,
  useMembersQuery,
  useRemoveMemberMutation,
  useUpdateMemberRoleMutation,
} from './api';
import type { SubscriptionRole } from './types';

interface MembersDialogProps {
  calendarId: string;
  open: boolean;
  onClose: () => void;
}

export default function MembersDialog({
  calendarId,
  open,
  onClose,
}: MembersDialogProps): ReactElement | null {
  const { t } = useTranslation();
  const wsId = useWorkspace((s) => s.workspaceId) ?? '';
  const { data: members, isLoading } = useMembersQuery(wsId, calendarId, open);
  const addMutation = useAddMemberMutation(wsId, calendarId);
  const updateRoleMutation = useUpdateMemberRoleMutation(wsId, calendarId);
  const removeMutation = useRemoveMemberMutation(wsId, calendarId);

  const roles: { value: SubscriptionRole; label: string }[] = [
    { value: 'owner', label: t('members.roleOwner') },
    { value: 'manager', label: t('members.roleManager') },
    { value: 'editor', label: t('members.roleEditor') },
    { value: 'viewer', label: t('members.roleViewer') },
  ];

  const [email, setEmail] = useState('');
  const [role, setRole] = useState<SubscriptionRole>('editor');

  const handleAdd = useCallback(
    (e: FormEvent) => {
      e.preventDefault();
      if (!email.trim()) return;
      addMutation.mutate(
        { email: email.trim(), role },
        {
          onSuccess: () => setEmail(''),
        },
      );
    },
    [email, role, addMutation],
  );

  return (
    <Dialog open={open} onClose={onClose} title={t('members.title')} fullScreenOnMobile>
      <form
        onSubmit={handleAdd}
        style={{ display: 'flex', gap: 'var(--nf-space-2)', marginBlockEnd: 'var(--nf-space-4)' }}
      >
        <Input
          type="email"
          placeholder={t('members.emailPlaceholder')}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          style={{ flex: 1 }}
        />
        <Select value={role} onChange={(e) => setRole(e.target.value as SubscriptionRole)}>
          {roles
            .filter((r) => r.value !== 'owner')
            .map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
        </Select>
        <Button type="submit" variant="primary" size="sm" disabled={addMutation.isPending}>
          {t('common.add')}
        </Button>
      </form>

      {addMutation.isError ? (
        <p
          style={{
            marginBlockEnd: 'var(--nf-space-2)',
            fontSize: 'var(--nf-text-xs)',
            color: 'var(--nf-color-danger)',
          }}
        >
          {addMutation.error.message}
        </p>
      ) : null}

      <div
        style={{
          maxBlockSize: '16rem',
          overflowY: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-2)',
        }}
      >
        {isLoading ? (
          <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
            {t('common.loading')}
          </p>
        ) : (
          members?.map((member) => (
            <div
              key={member.userId}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--nf-space-3)',
                borderRadius: 'var(--nf-radius-sm)',
                padding: 'var(--nf-space-1) var(--nf-space-2)',
              }}
            >
              <span
                style={{
                  display: 'inline-block',
                  width: '0.75rem',
                  height: '0.75rem',
                  flexShrink: 0,
                  borderRadius: 'var(--nf-radius-pill)',
                  backgroundColor: member.memberColor,
                }}
              />
              <div style={{ flex: 1, minWidth: 0 }}>
                <p
                  style={{
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    fontSize: 'var(--nf-text-sm)',
                    fontWeight: 'var(--nf-weight-medium)',
                    color: 'var(--nf-color-fg)',
                  }}
                >
                  {member.displayName}
                </p>
              </div>
              <Select
                value={member.role}
                onChange={(e) =>
                  updateRoleMutation.mutate({
                    userId: member.userId,
                    role: e.target.value as SubscriptionRole,
                  })
                }
                style={{ fontSize: 'var(--nf-text-xs)' }}
              >
                {roles.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </Select>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => removeMutation.mutate(member.userId)}
                aria-label={t('members.remove')}
              >
                <X size={16} />
              </Button>
            </div>
          ))
        )}
      </div>
    </Dialog>
  );
}
