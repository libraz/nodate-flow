import { type ReactElement, useCallback } from 'react';

import { useWorkspaceStore } from '../../stores/workspace-store';
import { useUpdateRsvpMutation } from './api';
import type { Rsvp } from './types';

const RSVP_OPTIONS: { value: Rsvp; label: string }[] = [
  { value: 'accepted', label: 'Accept' },
  { value: 'declined', label: 'Decline' },
  { value: 'tentative', label: 'Tentative' },
  { value: 'pending', label: 'Pending' },
];

interface RsvpButtonsProps {
  calendarId: string;
  eventId: string;
  currentRsvp: Rsvp;
  onUpdate?: (rsvp: Rsvp) => void;
}

export default function RsvpButtons({
  calendarId,
  eventId,
  currentRsvp,
  onUpdate,
}: RsvpButtonsProps): ReactElement {
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
  const mutation = useUpdateRsvpMutation(wsId, calendarId, eventId);

  const handleClick = useCallback(
    (rsvp: Rsvp) => {
      if (rsvp === currentRsvp) return;
      mutation.mutate(rsvp, {
        onSuccess: () => onUpdate?.(rsvp),
      });
    },
    [currentRsvp, mutation, onUpdate],
  );

  return (
    <div className="flex gap-1">
      {RSVP_OPTIONS.map((opt) => {
        const isActive = currentRsvp === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => handleClick(opt.value)}
            disabled={mutation.isPending}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              isActive ? 'bg-blue-600 text-white' : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
            } disabled:opacity-50`}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
