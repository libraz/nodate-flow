/**
 * EventFromTaskDialog — modal that asks the actor which workspace +
 * calendar should host the new event derived from the current task.
 *
 * The backend's `POST /workspaces/{wsId}/calendars/{calId}/events/from-task`
 * derives the event's title, due time and timezone from the task itself,
 * so this dialog only collects the calendar destination (workspace +
 * calendar). Once the mutation resolves we surface a success toast and
 * close — the task's timeline already invalidates through the mutation
 * hook, so the linked event row appears without further work here.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { useCalendarsQuery } from '../../calendar-events/api';
import { useWorkspacesQuery } from '../../workspaces/api';
import { useCreateEventFromTaskMutation } from './api';

export interface EventFromTaskDialogProps {
  taskId: string;
  /** Workspace the task lives in — used as the default destination. */
  defaultWorkspaceId: string;
  open: boolean;
  onClose: () => void;
}

export default function EventFromTaskDialog({
  taskId,
  defaultWorkspaceId,
  open,
  onClose,
}: EventFromTaskDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const create = useCreateEventFromTaskMutation();

  const [workspaceId, setWorkspaceId] = useState<string>(defaultWorkspaceId);
  const [calendarId, setCalendarId] = useState<string>('');
  const [calendarError, setCalendarError] = useState<string | undefined>(undefined);

  const calendarsQuery = useCalendarsQuery(workspaceId);
  const calendars = calendarsQuery.data ?? [];

  // Default the calendar to the first writable one whenever the workspace
  // changes (or on first open). Falls through to the first calendar
  // available when no role information is exposed for the actor.
  useEffect(() => {
    if (!open) return;
    if (calendars.length === 0) {
      setCalendarId('');
      return;
    }
    setCalendarId((prev) => {
      if (prev && calendars.some((c) => c.id === prev)) return prev;
      const writable = calendars.find((c) => {
        const role = (c as { role?: string }).role ?? '';
        return role === 'owner' || role === 'manager' || role === 'editor';
      });
      return (writable ?? calendars[0])?.id ?? '';
    });
  }, [open, calendars]);

  // Reset dialog state when re-opening for a different task or workspace.
  useEffect(() => {
    if (open) {
      setWorkspaceId(defaultWorkspaceId);
      setCalendarError(undefined);
    }
  }, [open, defaultWorkspaceId]);

  const workspaceOptions = useMemo<ComboboxOption[]>(
    () => workspaces.map((w) => ({ value: w.id, label: w.name })),
    [workspaces],
  );

  const calendarOptions = useMemo<ComboboxOption[]>(
    () => calendars.map((c) => ({ value: c.id, label: c.name })),
    [calendars],
  );

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (!calendarId) {
      setCalendarError(t('tasks.actions.create_event.calendar_required'));
      return;
    }
    setCalendarError(undefined);
    create.mutate(
      { workspaceId, calendarId, taskId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('tasks.actions.create_event.success') });
          onClose();
        },
        onError: (err) => {
          const message = formatApiError(err, t, 'tasks.actions.create_event.error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <Dialog open={open} onClose={onClose} title={t('tasks.actions.create_event.title')}>
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
      >
        <FormField label={t('tasks.actions.create_event.workspace_label')}>
          {(control) => (
            <Combobox
              {...control}
              value={workspaceId}
              onChange={(v) => setWorkspaceId(v)}
              options={workspaceOptions}
              placeholder={t('tasks.actions.create_event.workspace_placeholder')}
              aria-label={t('tasks.actions.create_event.workspace_label')}
            />
          )}
        </FormField>
        <FormField
          label={t('tasks.actions.create_event.calendar_label')}
          error={calendarError}
          description={
            calendarsQuery.isPending && !calendarsQuery.data
              ? t('tasks.actions.create_event.calendars_loading')
              : calendars.length === 0
                ? t('tasks.actions.create_event.no_calendars')
                : undefined
          }
        >
          {(control) => (
            <Combobox
              {...control}
              value={calendarId}
              onChange={(v) => {
                setCalendarId(v);
                setCalendarError(undefined);
              }}
              options={calendarOptions}
              placeholder={t('tasks.actions.create_event.calendar_placeholder')}
              aria-label={t('tasks.actions.create_event.calendar_label')}
              disabled={calendars.length === 0}
            />
          )}
        </FormField>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-xs)' }}>
          {t('tasks.actions.create_event.hint')}
        </p>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.5rem' }}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            {t('tasks.actions.create_event.cancel')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={create.isPending || calendars.length === 0}
          >
            {t('tasks.actions.create_event.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
