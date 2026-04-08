/**
 * WorkspaceSettingsForm — edit name, slug, description, and icon URL.
 *
 * Suspense-ready: reads the workspace via `useWorkspaceQuery` (suspense mode).
 * Validation is performed inline (no Zod dependency in this small form).
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type PatchWorkspaceInput, useUpdateWorkspace, useWorkspaceQuery } from './api';

export interface WorkspaceSettingsFormProps {
  workspaceId: string;
}

const SLUG_RE = /^[a-z0-9-]+$/;

function isValidUrl(value: string): boolean {
  try {
    // eslint-disable-next-line no-new
    new URL(value);
    return true;
  } catch {
    return false;
  }
}

export default function WorkspaceSettingsForm({
  workspaceId,
}: WorkspaceSettingsFormProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  const update = useUpdateWorkspace();

  const [name, setName] = useState(workspace.name);
  const [slug, setSlug] = useState(workspace.slug);
  const [description, setDescription] = useState(workspace.description ?? '');
  const [iconUrl, setIconUrl] = useState(workspace.iconUrl ?? '');
  const [submitting, setSubmitting] = useState(false);
  const [errors, setErrors] = useState<{
    name?: string;
    slug?: string;
    iconUrl?: string;
  }>({});

  const validate = (): boolean => {
    const next: typeof errors = {};
    if (name.trim() === '') {
      next.name = t('workspace.general.validation.name_required');
    }
    if (slug.trim() === '') {
      next.slug = t('workspace.general.validation.slug_required');
    } else if (!SLUG_RE.test(slug.trim())) {
      next.slug = t('workspace.general.validation.slug_format');
    }
    if (iconUrl.trim() !== '' && !isValidUrl(iconUrl.trim())) {
      next.iconUrl = t('workspace.general.validation.icon_url_format');
    }
    setErrors(next);
    return Object.keys(next).length === 0;
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!validate()) return;
    setSubmitting(true);
    const trimmedDesc = description.trim();
    const trimmedIcon = iconUrl.trim();
    const patch: PatchWorkspaceInput = {
      name: name.trim(),
      slug: slug.trim(),
      ...(trimmedDesc !== '' ? { description: trimmedDesc } : {}),
      ...(trimmedIcon !== '' ? { iconUrl: trimmedIcon } : {}),
    };
    try {
      await update.mutateAsync({ id: workspaceId, patch });
      toaster.show({ tone: 'success', message: t('workspace.general.saved') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.general.errors.update_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <h1 style={{ margin: 0, fontSize: '1.5rem' }}>{t('workspace.general.title')}</h1>
        <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.875rem' }}>
          {t('workspace.general.description')}
        </p>
      </header>

      <FormField
        label={t('workspace.general.field.name')}
        required
        {...(errors.name !== undefined ? { error: errors.name } : {})}
      >
        {(control) => (
          <Input
            {...control}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
          />
        )}
      </FormField>

      <FormField
        label={t('workspace.general.field.slug')}
        description={t('workspace.general.field.slug_help')}
        required
        {...(errors.slug !== undefined ? { error: errors.slug } : {})}
      >
        {(control) => (
          <Input
            {...control}
            value={slug}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => {
              setSlug(e.target.value);
            }}
          />
        )}
      </FormField>

      <FormField label={t('workspace.general.field.description')}>
        {(control) => (
          <Textarea
            {...control}
            value={description}
            rows={4}
            onChange={(e) => {
              setDescription(e.target.value);
            }}
          />
        )}
      </FormField>

      <FormField
        label={t('workspace.general.field.icon_url')}
        {...(errors.iconUrl !== undefined ? { error: errors.iconUrl } : {})}
      >
        {(control) => (
          <Input
            {...control}
            type="url"
            value={iconUrl}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => {
              setIconUrl(e.target.value);
            }}
          />
        )}
      </FormField>

      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? t('workspace.general.saving') : t('workspace.general.save')}
        </Button>
      </div>
    </form>
  );
}
