import { type ReactElement, useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';

import { useWorkspace } from '../../stores/workspace-store';
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
  const wsId = useWorkspace((s) => s.workspaceId) ?? '';
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
    <div style={{ display: 'flex', gap: 'var(--nf-space-1)' }}>
      {rsvpOptions.map((opt) => {
        const isActive = currentRsvp === opt.value;
        return (
          <Button
            key={opt.value}
            variant={isActive ? 'primary' : 'default'}
            size="sm"
            onClick={() => handleClick(opt.value)}
            disabled={mutation.isPending}
          >
            {opt.label}
          </Button>
        );
      })}
    </div>
  );
}
