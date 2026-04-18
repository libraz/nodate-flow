import { X } from 'lucide-react';
import { type FormEvent, type ReactElement, useCallback, useState } from 'react';

import { useWorkspaceStore } from '../../stores/workspace-store';
import {
  useAddMemberMutation,
  useMembersQuery,
  useRemoveMemberMutation,
  useUpdateMemberRoleMutation,
} from './api';
import type { SubscriptionRole } from './types';

const ROLES: { value: SubscriptionRole; label: string }[] = [
  { value: 'owner', label: 'Owner' },
  { value: 'manager', label: 'Manager' },
  { value: 'editor', label: 'Editor' },
  { value: 'viewer', label: 'Viewer' },
];

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
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
  const { data: members, isLoading } = useMembersQuery(wsId, calendarId, open);
  const addMutation = useAddMemberMutation(wsId, calendarId);
  const updateRoleMutation = useUpdateMemberRoleMutation(wsId, calendarId);
  const removeMutation = useRemoveMemberMutation(wsId, calendarId);

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Members</h2>
          <button type="button" onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="h-5 w-5" />
          </button>
        </div>

        <form onSubmit={handleAdd} className="mb-4 flex gap-2">
          <input
            type="email"
            placeholder="Email address"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <select
            value={role}
            onChange={(e) => setRole(e.target.value as SubscriptionRole)}
            className="rounded-md border border-gray-300 px-2 py-2 text-sm"
          >
            {ROLES.filter((r) => r.value !== 'owner').map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
          <button
            type="submit"
            disabled={addMutation.isPending}
            className="rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            Add
          </button>
        </form>

        {addMutation.isError ? (
          <p className="mb-2 text-xs text-red-500">{addMutation.error.message}</p>
        ) : null}

        <div className="max-h-64 space-y-2 overflow-y-auto">
          {isLoading ? (
            <p className="text-sm text-gray-500">Loading...</p>
          ) : (
            members?.map((member) => (
              <div key={member.userId} className="flex items-center gap-3 rounded-md px-2 py-1.5">
                <span
                  className="h-3 w-3 shrink-0 rounded-full"
                  style={{ backgroundColor: member.memberColor }}
                />
                <div className="flex-1 min-w-0">
                  <p className="truncate text-sm font-medium">{member.displayName}</p>
                </div>
                <select
                  value={member.role}
                  onChange={(e) =>
                    updateRoleMutation.mutate({
                      userId: member.userId,
                      role: e.target.value as SubscriptionRole,
                    })
                  }
                  className="rounded border border-gray-200 px-1.5 py-0.5 text-xs"
                >
                  {ROLES.map((r) => (
                    <option key={r.value} value={r.value}>
                      {r.label}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  onClick={() => removeMutation.mutate(member.userId)}
                  className="text-gray-400 hover:text-red-500"
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
