/**
 * WorkspaceCreateDialog — modal form to create a new workspace.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import FormActions from '../../components/form-actions';
import { onSubmitShortcut } from '../../lib/submit-shortcut';
import { createSlugField, slugify, slugOrRandom } from '../../lib/validation/identifier';
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
  const navigate = useNavigate();

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const reset = (): void => {
    setName('');
    setSlug('');
    setSlugTouched(false);
    setDescription('');
    setErrors({});
    setAdvancedOpen(false);
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
      slug: slug || slugOrRandom(name, 'workspace'),
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
      if (next.slug) setAdvancedOpen(true);
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
      await navigate({ to: '/' });
    } catch (err) {
      const code =
        err && typeof err === 'object' && 'code' in err
          ? (err as { code?: string }).code
          : undefined;
      if (code === 'WS.WORKSPACE.SLUG_ALREADY_TAKEN') {
        setErrors((prev) => ({ ...prev, slug: 'workspaces.validation.slug_taken' }));
        setAdvancedOpen(true);
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
        onKeyDown={onSubmitShortcut}
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

        <details
          open={advancedOpen}
          onToggle={(e) => {
            setAdvancedOpen(e.currentTarget.open);
          }}
        >
          <summary
            style={{
              cursor: 'pointer',
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm)',
              marginBlockEnd: 'var(--nf-space-3)',
            }}
          >
            {t('workspaces.form.advanced')}
          </summary>
          <FormField
            label={t('workspaces.form.slug')}
            description={t('workspaces.form.slug_hint')}
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
        </details>

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

        <FormActions hint={{ disabled: submitting }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('workspaces.form.cancel')}
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={submitting}
            aria-busy={submitting || undefined}
          >
            {t('workspaces.form.submit')}
          </Button>
        </FormActions>
      </form>
    </Dialog>
  );
}
