import { X } from 'lucide-react';
import { type FormEvent, type ReactElement, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceStore } from '../../stores/workspace-store';
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
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
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

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--nf-color-overlay)]">
      <div className="glass-surface-heavy w-full max-w-md rounded-[var(--nf-radius-lg)] p-6 ring-1 ring-[var(--nf-color-border)]">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold" style={{ color: 'var(--nf-color-fg)' }}>
            {t('members.title')}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="transition-colors"
            style={{ color: 'var(--nf-color-fg-subtle)' }}
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <form onSubmit={handleAdd} className="mb-4 flex gap-2">
          <input
            type="email"
            placeholder={t('members.emailPlaceholder')}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="flex-1 rounded-[var(--nf-radius-sm)] border border-[var(--nf-color-border)] px-3 py-2 text-sm outline-none focus:border-[var(--nf-color-accent)] focus:ring-1 focus:ring-[var(--nf-color-accent)]"
            style={{
              backgroundColor: 'var(--nf-color-bg-sunken)',
              color: 'var(--nf-color-fg)',
            }}
          />
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as SubscriptionRole)}
            className="rounded-[var(--nf-radius-sm)] border border-[var(--nf-color-border)] px-2 py-2 text-sm"
            style={{
              backgroundColor: 'var(--nf-color-bg-sunken)',
              color: 'var(--nf-color-fg)',
            }}
          >
            {roles
              .filter((r) => r.value !== 'owner')
              .map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
          </select>
          <button
            type="submit"
            disabled={addMutation.isPending}
            className="rounded-[var(--nf-radius-sm)] bg-[var(--nf-color-accent)] px-3 py-2 text-sm font-medium text-[var(--nf-color-fg-on-accent)] hover:opacity-90 disabled:opacity-50"
          >
            {t('common.add')}
          </button>
        </form>

        {addMutation.isError ? (
          <p className="mb-2 text-xs" style={{ color: 'var(--nf-color-danger)' }}>
            {addMutation.error.message}
          </p>
        ) : null}

        <div className="max-h-64 space-y-2 overflow-y-auto">
          {isLoading ? (
            <p className="text-sm" style={{ color: 'var(--nf-color-fg-muted)' }}>
              {t('common.loading')}
            </p>
          ) : (
            members?.map((member) => (
              <div
                key={member.userId}
                className="flex items-center gap-3 rounded-[var(--nf-radius-sm)] px-2 py-1.5 hover:bg-[var(--nf-color-surface-hover)]"
              >
                <span
                  className="h-3 w-3 shrink-0 rounded-full"
                  style={{ backgroundColor: member.memberColor }}
                />
                <div className="flex-1 min-w-0">
                  <p
                    className="truncate text-sm font-medium"
                    style={{ color: 'var(--nf-color-fg)' }}
                  >
                    {member.displayName}
                  </p>
                </div>
                <select
                  value={member.role}
                  onChange={(e) =>
                    updateRoleMutation.mutate({
                      userId: member.userId,
                      role: e.target.value as SubscriptionRole,
                    })
                  }
                  className="rounded-[var(--nf-radius-sm)] border border-[var(--nf-color-border)] px-1.5 py-0.5 text-xs"
                  style={{
                    backgroundColor: 'var(--nf-color-bg-sunken)',
                    color: 'var(--nf-color-fg-muted)',
                  }}
                >
                  {roles.map((r) => (
                    <option key={r.value} value={r.value}>
                      {r.label}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  onClick={() => removeMutation.mutate(member.userId)}
                  className="hover:text-[var(--nf-color-danger)] transition-colors"
                  style={{ color: 'var(--nf-color-fg-subtle)' }}
                >
                  <X className="h-4 w-4" />
                </button>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
