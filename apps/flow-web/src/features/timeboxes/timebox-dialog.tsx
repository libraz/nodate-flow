/**
 * @file Create / edit dialog for a workspace timebox.
 *
 * Mounted from {@link TimeboxesPage}. The same component handles both
 * `create` and `edit` modes; the call site supplies the optional
 * `initial` timebox to pre-fill fields and switch the submit verb.
 *
 * Validation lives inline (not Zod) because the schema is small and
 * the only cross-field rule is "endsOn must be on or after startsOn".
 * All field labels and the cross-field error are translated.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useProjectsQuery } from '../projects/api';
import { type Timebox, useCreateTimeboxMutation, useUpdateTimeboxMutation } from './api';
import styles from './timeboxes-page.module.css';

export type TimeboxDialogMode = 'create' | 'edit';

export interface TimeboxDialogProps {
  /** Owning workspace id (path param). */
  workspaceId: string;
  /** Mode flag — `create` opens a blank form, `edit` pre-fills. */
  mode: TimeboxDialogMode;
  /** Existing timebox to edit. Required when `mode === 'edit'`. */
  initial?: Timebox;
  /** Whether the dialog is currently open. */
  open: boolean;
  /** Called when the dialog requests close (escape, overlay, cancel). */
  onClose: () => void;
}

interface FieldErrors {
  name?: string;
  startsOn?: string;
  endsOn?: string;
}

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Validate the form. Returns the cleaned payload + any field-level
 * errors. The error map is empty when the form is submittable.
 */
function validate(
  name: string,
  description: string,
  startsOn: string,
  endsOn: string,
  t: (key: string) => string,
): {
  errors: FieldErrors;
  clean: { name: string; description: string; startsOn: string; endsOn: string };
} {
  const errors: FieldErrors = {};
  const trimmedName = name.trim();
  if (trimmedName.length === 0) {
    errors.name = t('timeboxes.field.name');
  }
  if (!DATE_RE.test(startsOn)) {
    errors.startsOn = t('timeboxes.field.starts_on');
  }
  if (!DATE_RE.test(endsOn)) {
    errors.endsOn = t('timeboxes.field.ends_on');
  }
  if (!errors.startsOn && !errors.endsOn && endsOn < startsOn) {
    errors.endsOn = t('timeboxes.dialog.invalid_dates');
  }
  return {
    errors,
    clean: {
      name: trimmedName,
      description: description.trim(),
      startsOn,
      endsOn,
    },
  };
}

export default function TimeboxDialog({
  workspaceId,
  mode,
  initial,
  open,
  onClose,
}: TimeboxDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const create = useCreateTimeboxMutation();
  const update = useUpdateTimeboxMutation();
  // The project picker is read-only metadata on edit; we still load it
  // for create so the user can scope a new timebox to a project.
  const { data: projects } = useProjectsQuery(workspaceId);

  // Render-key reset: re-mount field state whenever the dialog opens
  // for a new target (different mode or different initial timebox).
  // Setting state during render in response to a prop change is the
  // sanctioned pattern; an effect would lag a render behind.
  const targetKey = mode === 'edit' && initial ? `edit:${initial.id}` : 'create';
  const [trackedKey, setTrackedKey] = useState(targetKey);
  const [name, setName] = useState(() => initial?.name ?? '');
  const [description, setDescription] = useState(() => initial?.description ?? '');
  const [startsOn, setStartsOn] = useState(() => initial?.startsOn ?? '');
  const [endsOn, setEndsOn] = useState(() => initial?.endsOn ?? '');
  const [projectId, setProjectId] = useState(() => initial?.projectId ?? '');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  if (open && trackedKey !== targetKey) {
    setTrackedKey(targetKey);
    setName(initial?.name ?? '');
    setDescription(initial?.description ?? '');
    setStartsOn(initial?.startsOn ?? '');
    setEndsOn(initial?.endsOn ?? '');
    setProjectId(initial?.projectId ?? '');
    setErrors({});
  }

  const titleKey =
    mode === 'edit' ? 'timeboxes.dialog.edit.title' : 'timeboxes.dialog.create.title';
  const submitKey =
    mode === 'edit' ? 'timeboxes.dialog.submit.edit' : 'timeboxes.dialog.submit.create';

  const handleClose = (): void => {
    if (submitting) return;
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const { errors: nextErrors, clean } = validate(name, description, startsOn, endsOn, t);
    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      if (mode === 'edit' && initial) {
        await update.mutateAsync({
          wsId: workspaceId,
          timeboxId: initial.id,
          body: {
            name: clean.name,
            description: clean.description,
            startsOn: clean.startsOn,
            endsOn: clean.endsOn,
          },
        });
        toaster.show({ tone: 'success', message: t('timeboxes.dialog.edit.success') });
      } else {
        await create.mutateAsync({
          wsId: workspaceId,
          body: {
            name: clean.name,
            startsOn: clean.startsOn,
            endsOn: clean.endsOn,
            ...(clean.description ? { description: clean.description } : {}),
            ...(projectId ? { projectId } : {}),
          },
        });
        toaster.show({ tone: 'success', message: t('timeboxes.dialog.create.success') });
      }
      onClose();
    } catch {
      toaster.show({
        tone: 'danger',
        message:
          mode === 'edit' ? t('timeboxes.dialog.edit.error') : t('timeboxes.dialog.create.error'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t(titleKey)} size="lg">
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        className={styles.form}
      >
        <FormField
          label={t('timeboxes.field.name')}
          required
          {...(errors.name ? { error: errors.name } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              autoFocus
            />
          )}
        </FormField>

        <FormField label={t('timeboxes.field.description')}>
          {(control) => (
            <Textarea
              {...control}
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
              rows={3}
            />
          )}
        </FormField>

        <div className={styles.formDateRow}>
          <FormField
            label={t('timeboxes.field.starts_on')}
            required
            {...(errors.startsOn ? { error: errors.startsOn } : {})}
          >
            {(control) => (
              <Input
                {...control}
                type="date"
                value={startsOn}
                onChange={(e) => {
                  setStartsOn(e.target.value);
                }}
              />
            )}
          </FormField>
          <FormField
            label={t('timeboxes.field.ends_on')}
            required
            {...(errors.endsOn ? { error: errors.endsOn } : {})}
          >
            {(control) => (
              <Input
                {...control}
                type="date"
                value={endsOn}
                onChange={(e) => {
                  setEndsOn(e.target.value);
                }}
              />
            )}
          </FormField>
        </div>

        {mode === 'create' ? (
          <FormField label={t('timeboxes.field.project.label')}>
            {(control) => (
              <Select
                {...control}
                value={projectId}
                onChange={(e) => {
                  setProjectId(e.target.value);
                }}
              >
                <option value="">{t('timeboxes.field.project.placeholder')}</option>
                {projects
                  .filter((p) => !p.isArchived)
                  .map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
              </Select>
            )}
          </FormField>
        ) : null}

        <div className={styles.formActions}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('timeboxes.dialog.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t(submitKey)}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
