/**
 * ProjectCreateDialog — modal form to create a new project in a workspace.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { useCreateProject } from './api';

export interface ProjectCreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  name?: string;
  slug?: string;
  description?: string;
}

const schema = z.object({
  name: z.string().min(1, 'projects.validation.name_required').max(100),
  slug: z
    .string()
    .min(1, 'projects.validation.slug_required')
    .max(64)
    .regex(/^[a-z0-9-]+$/, 'projects.validation.slug_format'),
  description: z.string().max(500).optional(),
});

export default function ProjectCreateDialog({
  workspaceId,
  open,
  onClose,
}: ProjectCreateDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const create = useCreateProject();

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [description, setDescription] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setName('');
    setSlug('');
    setDescription('');
    setErrors({});
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const parsed = schema.safeParse({
      name,
      slug,
      description: description.trim() === '' ? undefined : description,
    });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'name') next.name = issue.message;
        if (field === 'slug') next.slug = issue.message;
        if (field === 'description') next.description = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      await create.mutateAsync({
        workspaceId,
        input: {
          name: parsed.data.name,
          slug: parsed.data.slug,
          ...(parsed.data.description ? { description: parsed.data.description } : {}),
        },
      });
      reset();
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('projects.errors.create_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('projects.new')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
        <FormField
          label={t('projects.form.name')}
          required
          {...(errors.name ? { error: t(errors.name) } : {})}
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

        <FormField
          label={t('projects.form.key')}
          required
          {...(errors.slug ? { error: t(errors.slug) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField
          label={t('projects.form.description')}
          {...(errors.description ? { error: t(errors.description) } : {})}
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('projects.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('projects.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
