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

import { createIdentifierField, createSlugField, slugify } from '../../lib/validation/identifier';
import { useCreateProject } from './api';

export interface ProjectCreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  name?: string;
  slug?: string;
  identifier?: string;
  description?: string;
}

const schema = z.object({
  name: z.string().min(1, 'projects.validation.name_required').max(100),
  slug: createSlugField({
    requiredKey: 'projects.validation.slug_required',
    formatKey: 'projects.validation.slug_format',
  }),
  identifier: createIdentifierField(),
  description: z.string().max(500).optional(),
});

export default function ProjectCreateDialog({
  workspaceId,
  open,
  onClose,
}: ProjectCreateDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const { t: tLabels } = useTranslation('labels');
  const create = useCreateProject();

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [identifier, setIdentifier] = useState('');
  const [identifierTouched, setIdentifierTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setName('');
    setSlug('');
    setSlugTouched(false);
    setIdentifier('');
    setIdentifierTouched(false);
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
      identifier,
      description: description.trim() === '' ? undefined : description,
    });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'name') next.name ??= issue.message;
        if (field === 'slug') next.slug ??= issue.message;
        if (field === 'identifier') next.identifier ??= issue.message;
        if (field === 'description') next.description ??= issue.message;
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
          identifier: parsed.data.identifier,
          ...(parsed.data.description ? { description: parsed.data.description } : {}),
        },
      });
      reset();
      onClose();
    } catch (err) {
      const code =
        err && typeof err === 'object' && 'code' in err
          ? (err as { code?: string }).code
          : undefined;
      if (code === 'WS.PROJECT.SLUG_ALREADY_TAKEN') {
        setErrors((prev) => ({ ...prev, slug: 'projects.validation.slug_taken' }));
      } else if (code === 'WS.PROJECT.IDENTIFIER_ALREADY_TAKEN') {
        setErrors((prev) => ({ ...prev, identifier: 'identifier.validation.taken' }));
      } else {
        toaster.show({ tone: 'danger', message: t('projects.errors.create_failed') });
      }
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
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
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
                const nextName = e.target.value;
                setName(nextName);
                if (!slugTouched) setSlug(slugify(nextName));
                if (!identifierTouched) {
                  setIdentifier(
                    nextName
                      .replace(/[^a-zA-Z]/g, '')
                      .slice(0, 3)
                      .toUpperCase(),
                  );
                }
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
                setSlugTouched(true);
                setSlug(e.target.value);
              }}
            />
          )}
        </FormField>

        <FormField
          label={tLabels('identifier.label')}
          required
          description={tLabels('identifier.hint')}
          {...(errors.identifier ? { error: tLabels(errors.identifier) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={identifier}
              onChange={(e) => {
                setIdentifierTouched(true);
                setIdentifier(e.target.value.toUpperCase().slice(0, 5));
              }}
              placeholder={tLabels('identifier.placeholder')}
              maxLength={5}
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--nf-space-3)' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('projects.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? t('projects.form.submitting') : t('projects.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
