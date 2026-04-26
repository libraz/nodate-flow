/**
 * IntakeConvertDialog — modal that asks the actor which project to drop
 * an intake item into when promoting it to a task.
 *
 * Rendered from {@link IntakeList} when the user clicks the "Convert"
 * row action. The dialog is intentionally minimal: backend's
 * `POST /workspaces/{wsId}/intake/{id}/convert` only consumes a
 * `projectId`, so we expose a single Combobox plus submit/cancel.
 *
 * Project options are scoped to the active workspace via
 * `useProjectsQuery`; loading state for that query is handled by the
 * Suspense boundary the consumer mounts above the dialog. Once the
 * mutation resolves we surface a success toast that links the user to
 * the freshly created task and close.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Combobox, { type ComboboxOption } from '@nodate-flow/ui/primitives/combobox';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { useProjectsQuery } from '../../projects/api';
import { type IntakeItem, useConvertIntakeItemMutation } from './api';

export interface IntakeConvertDialogProps {
  workspaceId: string;
  item: IntakeItem | null;
  open: boolean;
  onClose: () => void;
}

export default function IntakeConvertDialog({
  workspaceId,
  item,
  open,
  onClose,
}: IntakeConvertDialogProps): ReactElement {
  const { t } = useTranslation('inbox');
  const { data: projects } = useProjectsQuery(workspaceId);
  const convert = useConvertIntakeItemMutation();
  const navigate = useNavigate();

  const [projectId, setProjectId] = useState<string>('');
  const [projectError, setProjectError] = useState<string | undefined>(undefined);

  // Reset selection whenever the dialog re-opens for a different item so
  // the picker doesn't carry over stale state from a prior conversion.
  useEffect(() => {
    if (open) {
      setProjectId(projects[0]?.id ?? '');
      setProjectError(undefined);
    }
  }, [open, projects]);

  const projectOptions = useMemo<ComboboxOption[]>(
    () =>
      projects.map((p) => ({
        value: p.id,
        label: p.identifier ? `${p.name} (${p.identifier})` : p.name,
      })),
    [projects],
  );

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!item) return;
    if (!projectId) {
      setProjectError(t('intake.convert.project_required'));
      return;
    }
    setProjectError(undefined);
    convert.mutate(
      { wsId: workspaceId, id: item.id, projectId },
      {
        onSuccess: ({ taskId }) => {
          toaster.show({ tone: 'success', message: t('intake.convert.success') });
          onClose();
          void navigate({ to: '/tasks/$taskId', params: { taskId } });
        },
        onError: (err) => {
          const message = formatApiError(err, t, 'intake.convert.error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <Dialog open={open} onClose={onClose} title={t('intake.convert.title')}>
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}
      >
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {item ? item.title : ''}
        </p>
        <FormField label={t('intake.convert.project_label')} error={projectError}>
          {(control) => (
            <Combobox
              {...control}
              value={projectId}
              onChange={(v) => {
                setProjectId(v);
                setProjectError(undefined);
              }}
              options={projectOptions}
              placeholder={t('intake.convert.project_placeholder')}
              aria-label={t('intake.convert.project_label')}
            />
          )}
        </FormField>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.5rem' }}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            {t('intake.convert.cancel')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={convert.isPending || projects.length === 0}
          >
            {t('intake.convert.submit')}
          </Button>
        </div>
        {projects.length === 0 ? (
          <p
            style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-xs)' }}
          >
            {t('intake.convert.no_projects')}
          </p>
        ) : null}
      </form>
    </Dialog>
  );
}
