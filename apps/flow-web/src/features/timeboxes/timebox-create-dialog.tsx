/**
 * TimeboxCreateDialog — modal form to create a new timebox in a workspace.
 *
 * Fields: name (required), description (optional), start date (required),
 * end date (required). Validates that the end date is after the start date.
 */

import Button from '@nodate-flow/ui/primitives/button';
import DatePicker from '@nodate-flow/ui/primitives/date-picker';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { useCreateTimebox } from './api';

export interface TimeboxCreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  name?: string;
  description?: string;
  startsOn?: string;
  endsOn?: string;
}

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

const schema = z
  .object({
    name: z.string().min(1, 'required'),
    description: z.string().max(500).optional(),
    startsOn: z.string().regex(DATE_RE, 'invalid_date'),
    endsOn: z.string().regex(DATE_RE, 'invalid_date'),
  })
  .refine((data) => data.endsOn > data.startsOn, {
    path: ['endsOn'],
    message: 'end_before_start',
  });

export default function TimeboxCreateDialog({
  workspaceId,
  open,
  onClose,
}: TimeboxCreateDialogProps): ReactElement {
  const { t } = useTranslation('timeboxes');
  const { t: tCommon } = useTranslation('common');
  const weekdayLabels = tCommon('common.date.weekdays', { returnObjects: true }) as string[];
  const formatMonthYear = (year: number, month: number): string =>
    tCommon('common.date.monthYear', { year, month });
  const create = useCreateTimebox(workspaceId);

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [startsOn, setStartsOn] = useState('');
  const [endsOn, setEndsOn] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setName('');
    setDescription('');
    setStartsOn('');
    setEndsOn('');
    setErrors({});
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  /** Map a Zod issue code to a translated error message. */
  const resolveError = (code: string): string => {
    switch (code) {
      case 'required':
        return t('name_label');
      case 'invalid_date':
        return t('starts_on_label');
      case 'end_before_start':
        return t('ends_on_label');
      default:
        return code;
    }
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const parsed = schema.safeParse({
      name: name.trim(),
      description: description.trim() === '' ? undefined : description.trim(),
      startsOn,
      endsOn,
    });

    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'name') next.name = resolveError(issue.message);
        if (field === 'description') next.description = resolveError(issue.message);
        if (field === 'startsOn') next.startsOn = resolveError(issue.message);
        if (field === 'endsOn') next.endsOn = resolveError(issue.message);
      }
      setErrors(next);
      return;
    }

    setErrors({});
    setSubmitting(true);
    try {
      await create.mutateAsync({
        input: {
          name: parsed.data.name,
          startsOn: parsed.data.startsOn,
          endsOn: parsed.data.endsOn,
          ...(parsed.data.description ? { description: parsed.data.description } : {}),
        },
      });
      reset();
      onClose();
      toaster.show({ tone: 'success', message: t('toast.created') });
    } catch {
      toaster.show({ tone: 'danger', message: t('toast.created') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('create')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        className="nf-timebox-form"
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
        <FormField
          label={t('name_label')}
          required
          {...(errors.name ? { error: errors.name } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={name}
              placeholder={t('name_placeholder')}
              onChange={(e) => {
                setName(e.target.value);
              }}
              autoFocus
            />
          )}
        </FormField>

        <FormField
          label={t('description_label')}
          {...(errors.description ? { error: errors.description } : {})}
        >
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

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
          <FormField
            label={t('starts_on_label')}
            required
            {...(errors.startsOn ? { error: errors.startsOn } : {})}
          >
            {() => (
              <DatePicker
                value={startsOn}
                onChange={setStartsOn}
                weekdayLabels={weekdayLabels}
                formatMonthYear={formatMonthYear}
                triggerLabel={startsOn || tCommon('common.date.placeholder')}
              />
            )}
          </FormField>

          <FormField
            label={t('ends_on_label')}
            required
            {...(errors.endsOn ? { error: errors.endsOn } : {})}
          >
            {() => (
              <DatePicker
                value={endsOn}
                onChange={setEndsOn}
                weekdayLabels={weekdayLabels}
                formatMonthYear={formatMonthYear}
                triggerLabel={endsOn || tCommon('common.date.placeholder')}
                {...(startsOn ? { minDate: startsOn } : {})}
              />
            )}
          </FormField>
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('edit')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('create')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
