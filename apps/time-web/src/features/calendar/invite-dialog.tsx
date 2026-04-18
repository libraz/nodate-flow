import { Check, Copy, X } from 'lucide-react';
import { type ReactElement, useCallback, useState } from 'react';

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Invite Links</h2>
          <button type="button" onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="mb-4 flex items-center gap-2">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="flex-1 rounded-md border border-gray-300 px-3 py-2 text-sm"
          >
            <option value="manager">Manager</option>
            <option value="editor">Editor</option>
            <option value="viewer">Viewer</option>
          </select>
          <button
            type="button"
            onClick={handleCreate}
            disabled={createMutation.isPending}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            Create Link
          </button>
        </div>

        <div className="max-h-64 space-y-3 overflow-y-auto">
          {isLoading ? (
            <p className="text-sm text-gray-500">Loading...</p>
          ) : invites?.length === 0 ? (
            <p className="text-sm text-gray-400">No active invite links</p>
          ) : (
            invites?.map((invite) => (
              <div
                key={invite.id}
                className="flex items-center justify-between rounded-md border border-gray-200 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs font-medium capitalize">
                      {invite.role}
                    </span>
                    <span className="text-xs text-gray-500">
                      {invite.useCount} use{invite.useCount !== 1 ? 's' : ''}
                      {invite.maxUses != null ? ` / ${invite.maxUses}` : ''}
                    </span>
                  </div>
                  {invite.expiresAt ? (
                    <p className="mt-0.5 text-xs text-gray-400">
                      Expires {new Date(invite.expiresAt).toLocaleDateString()}
                    </p>
                  ) : null}
                </div>
                <div className="ml-2 flex items-center gap-1">
                  <button
                    type="button"
                    onClick={() => handleCopy(invite.token)}
                    className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600"
                    title="Copy invite link"
                  >
                    {copiedToken === invite.token ? (
                      <Check className="h-4 w-4 text-green-500" />
                    ) : (
                      <Copy className="h-4 w-4" />
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => revokeMutation.mutate(invite.id)}
                    className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-red-500"
                    title="Revoke"
                  >
                    <X className="h-4 w-4" />
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
