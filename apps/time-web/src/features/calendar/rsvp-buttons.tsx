import { type ReactElement, useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import { useWorkspaceStore } from '../../stores/workspace-store';
import { useUpdateRsvpMutation } from './api';
import type { Rsvp } from './types';

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
  const { t } = useTranslation();
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
  const mutation = useUpdateRsvpMutation(wsId, calendarId, eventId);

  const rsvpOptions: { value: Rsvp; label: string }[] = [
    { value: 'accepted', label: t('rsvp.accept') },
    { value: 'declined', label: t('rsvp.decline') },
    { value: 'tentative', label: t('rsvp.tentative') },
    { value: 'pending', label: t('rsvp.pending') },
  ];

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
      {rsvpOptions.map((opt) => {
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
