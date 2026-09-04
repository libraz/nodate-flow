/**
 * WorkspaceCreateDialog — modal form to create a new workspace.
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

import { createSlugField, slugify } from '../../lib/validation/identifier';
import { useCreateWorkspace } from './api';

export interface WorkspaceCreateDialogProps {
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  name?: string;
  slug?: string;
  description?: string;
}

const schema = z.object({
  name: z.string().min(1, 'workspaces.validation.name_required').max(100),
  slug: createSlugField({
    requiredKey: 'workspaces.validation.slug_required',
    formatKey: 'workspaces.validation.slug_format',
  }),
  description: z.string().max(500).optional(),
});

export default function WorkspaceCreateDialog({
  open,
  onClose,
}: WorkspaceCreateDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const create = useCreateWorkspace();

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setName('');
    setSlug('');
    setSlugTouched(false);
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
        name: parsed.data.name,
        slug: parsed.data.slug,
        ...(parsed.data.description ? { description: parsed.data.description } : {}),
      });
      reset();
      onClose();
    } catch (err) {
      const code =
        err && typeof err === 'object' && 'code' in err
          ? (err as { code?: string }).code
          : undefined;
      if (code === 'WS.WORKSPACE.SLUG_ALREADY_TAKEN') {
        setErrors((prev) => ({ ...prev, slug: 'workspaces.validation.slug_taken' }));
      } else {
        toaster.show({ tone: 'danger', message: t('workspaces.errors.create_failed') });
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('workspaces.new')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
      >
        <FormField
          label={t('workspaces.form.name')}
          required
          {...(errors.name ? { error: t(errors.name) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={name}
              onChange={(e) => {
                const nextName = e.target.value;
                setName(nextName);
                // Mirror the name into slug while the user hasn't
                // manually edited the slug field. As soon as they
                // touch it, we stop overriding.
                if (!slugTouched) setSlug(slugify(nextName));
              }}
              autoFocus
            />
          )}
        </FormField>

        <FormField
          label={t('workspaces.form.slug')}
          required
          {...(errors.slug ? { error: t(errors.slug) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={slug}
              onChange={(e) => {
                setSlugTouched(true);
                setSlug(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField
          label={t('workspaces.form.description')}
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--nf-space-3)' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('workspaces.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? t('workspaces.form.submitting') : t('workspaces.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
