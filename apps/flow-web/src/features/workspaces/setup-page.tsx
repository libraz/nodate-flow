/**
 * SetupPage -- first-run onboarding surface for an authenticated user who
 * has no workspace yet. Renders a centered card with a minimal form
 * (name + slug) that drives the shared `useCreateWorkspace` mutation.
 *
 * Route guard lives in `_authenticated.setup.tsx`: if the user already
 * belongs to at least one workspace the loader redirects to /calendar
 * before this component is mounted. On successful creation we navigate
 * to /calendar so the new user lands on an immediately useful surface.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { useCreateWorkspace } from './api';

interface FieldErrors {
  name?: string;
  slug?: string;
}

const schema = z.object({
  name: z.string().min(1, 'workspaces.validation.name_required').max(100),
  slug: z
    .string()
    .min(1, 'workspaces.validation.slug_required')
    .max(64)
    .regex(/^[a-z0-9-]+$/, 'workspaces.validation.slug_format'),
});

/**
 * Derive a slug-safe string from the display name. Mirrors the logic in
 * `workspace-create-dialog.tsx` so the UX is consistent between the
 * onboarding route and the in-app "new workspace" dialog.
 */
function slugify(input: string): string {
  return input
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64);
}

export default function SetupPage(): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();
  const create = useCreateWorkspace();

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    const parsed = schema.safeParse({ name, slug });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (field === 'name') next.name = issue.message;
        if (field === 'slug') next.slug = issue.message;
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
      });
      await navigate({ to: '/calendar', replace: true });
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
    <section
      style={{
        minBlockSize: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        paddingBlock: 'clamp(2rem, 8vh, 6rem)',
        paddingInline: 'clamp(1rem, 4vw, 2rem)',
      }}
    >
      <div
        style={{
          inlineSize: 'min(28rem, 100%)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-5, 1.5rem)',
          padding: 'var(--nf-space-6, 2rem)',
          borderRadius: 'var(--nf-radius-lg, 0.75rem)',
          background: 'var(--nf-color-bg-elevated, var(--nf-color-surface))',
          border: '1px solid var(--nf-color-border, var(--nf-color-hairline))',
          boxShadow: 'var(--nf-shadow-md, 0 4px 16px rgba(0, 0, 0, 0.06))',
        }}
      >
        <header style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
          <h1
            style={{
              margin: 0,
              fontFamily: 'var(--font-display)',
              fontWeight: 500,
              fontSize: 'clamp(1.5rem, 3vw, 2rem)',
              lineHeight: 1.15,
              letterSpacing: '-0.01em',
              color: 'var(--nf-color-fg)',
            }}
          >
            {t('workspaces.setup.title')}
          </h1>
          <p
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm, 0.875rem)',
              lineHeight: 1.5,
            }}
          >
            {t('workspaces.setup.description')}
          </p>
        </header>

        <form
          onSubmit={(e) => {
            void handleSubmit(e);
          }}
          noValidate
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4, 1rem)' }}
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
                  if (!slugTouched) setSlug(slugify(nextName));
                }}
                placeholder={t('workspaces.setup.name_placeholder')}
                autoComplete="organization"
                autoFocus
              />
            )}
          </FormField>

          <FormField
            label={t('workspaces.form.slug')}
            required
            description={t('workspaces.setup.slug_hint')}
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
                placeholder={t('workspaces.setup.slug_placeholder')}
                autoComplete="off"
                spellCheck={false}
              />
            )}
          </FormField>

          <Button type="submit" variant="primary" disabled={submitting}>
            {submitting ? t('workspaces.setup.submitting') : t('workspaces.setup.submit')}
          </Button>
        </form>
      </div>
    </section>
  );
}
