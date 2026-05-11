/**
 * /workspaces/$wsId -- Edit per-workspace settings (name, slug, description,
 * timezone, country). Only exposes fields backed by PATCH /workspaces/{wsId}.
 */

import {
  SUPPORTED_COUNTRIES,
  type components,
  detectTimezone,
  formatTimezoneLabel,
  groupTimezonesByRegion,
} from '@nodate-flow/sdk';
import { useZodForm } from '@nodate-flow/ui/hooks/use-zod-form';
import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { Link, createFileRoute, useParams } from '@tanstack/react-router';
import { type ReactElement, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import AuthCard from '../../components/auth-card';
import { sdk } from '../../lib/sdk';

const schema = z.object({
  name: z.string().min(1, 'validation.workspace_name_required').max(100),
  slug: z
    .string()
    .min(1, 'validation.workspace_slug_required')
    .max(63)
    .regex(/^[a-z0-9-]+$/, 'validation.workspace_slug_invalid'),
  description: z.string().max(500).or(z.literal('')),
  timezone: z.string().min(1, 'validation.timezone_required'),
  country: z
    .string()
    .regex(/^([A-Z]{2})?$/, 'validation.country_invalid')
    .or(z.literal('')),
});

type FormValues = z.infer<typeof schema>;

/**
 * SDK-derived workspace shape; mirrors the Go `Workspace` schema exactly so
 * field-name drift between the API and this page is impossible.
 */
type Workspace = components['schemas']['Workspace'];

function WorkspaceEditPage(): ReactElement {
  const { t } = useTranslation('auth');
  const { wsId } = useParams({ from: '/_authenticated/workspaces_/$wsId' });
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [serverError, setServerError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const timezoneGroups = useMemo(() => groupTimezonesByRegion(), []);
  const countries = useMemo(
    () => Object.entries(SUPPORTED_COUNTRIES).sort(([, a], [, b]) => a.localeCompare(b)),
    [],
  );

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting, isDirty },
  } = useZodForm<typeof schema>(
    schema,
    {
      name: '',
      slug: '',
      description: '',
      timezone: detectTimezone(),
      country: '',
    },
    {
      values: workspace
        ? {
            name: workspace.name,
            slug: workspace.slug,
            description: workspace.description ?? '',
            timezone: workspace.timezone || detectTimezone(),
            country: workspace.country ?? '',
          }
        : {
            name: '',
            slug: '',
            description: '',
            timezone: detectTimezone(),
            country: '',
          },
    },
  );

  useEffect(() => {
    let cancelled = false;
    void sdk.GET('/workspaces/{wsId}', { params: { path: { wsId } } }).then((result) => {
      if (cancelled) return;
      if (result.error || !result.data) {
        setLoadError(t('errors.generic'));
        return;
      }
      setWorkspace(result.data as Workspace);
    });
    return () => {
      cancelled = true;
    };
  }, [wsId, t]);

  const onSubmit = async (values: FormValues): Promise<void> => {
    setServerError(null);
    setSuccess(false);
    try {
      const { data, error } = await sdk.PATCH('/workspaces/{wsId}', {
        params: { path: { wsId } },
        body: {
          name: values.name,
          slug: values.slug,
          description: values.description,
          timezone: values.timezone,
          country: values.country,
        },
      });
      if (error || !data) {
        setServerError(t('errors.generic'));
        return;
      }
      setWorkspace(data as Workspace);
      setSuccess(true);
    } catch {
      setServerError(t('errors.unknown'));
    }
  };

  if (loadError) {
    return (
      <AuthCard>
        <p role="alert" className="aw-error">
          {loadError}
        </p>
        <Link to="/workspaces">{t('workspaces.back')}</Link>
      </AuthCard>
    );
  }

  return (
    <AuthCard width="wide">
      <form
        onSubmit={(e) => {
          void handleSubmit(onSubmit)(e);
        }}
        noValidate
        className="aw-stack aw-stack-5"
      >
        <h1 className="aw-page-title">{t('workspaces.edit_title')}</h1>

        <FormField
          label={t('workspaces.name_label')}
          required
          {...(errors.name?.message ? { error: t(errors.name.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('name');
            return <Input {...control} {...field} ref={ref} type="text" />;
          }}
        </FormField>

        <FormField
          label={t('workspaces.slug_label')}
          required
          {...(errors.slug?.message ? { error: t(errors.slug.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('slug');
            return <Input {...control} {...field} ref={ref} type="text" />;
          }}
        </FormField>

        <FormField
          label={t('workspaces.description_label')}
          {...(errors.description?.message ? { error: t(errors.description.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('description');
            return <Input {...control} {...field} ref={ref} type="text" />;
          }}
        </FormField>

        <FormField
          label={t('profile.timezone')}
          {...(errors.timezone?.message ? { error: t(errors.timezone.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('timezone');
            return (
              <select {...control} {...field} ref={ref} className="aw-select">
                {timezoneGroups.map(({ region, zones }) => (
                  <optgroup key={region} label={region}>
                    {zones.map((tz) => (
                      <option key={tz} value={tz}>
                        {region === 'Global' ? tz : formatTimezoneLabel(tz)}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            );
          }}
        </FormField>

        <FormField
          label={t('profile.country')}
          {...(errors.country?.message ? { error: t(errors.country.message) } : {})}
        >
          {(control) => {
            const { ref, ...field } = register('country');
            return (
              <select {...control} {...field} ref={ref} className="aw-select">
                <option value="">{t('profile.country_unset')}</option>
                {countries.map(([code, name]) => (
                  <option key={code} value={code}>
                    {code} — {name}
                  </option>
                ))}
              </select>
            );
          }}
        </FormField>

        {serverError ? (
          <p role="alert" className="aw-error">
            {serverError}
          </p>
        ) : null}

        {success ? (
          <output
            style={{
              margin: 0,
              color: 'var(--nf-color-success)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('workspaces.saved')}
          </output>
        ) : null}

        <Button type="submit" variant="primary" disabled={isSubmitting || !isDirty}>
          {isSubmitting ? t('profile.saving') : t('profile.save')}
        </Button>

        <p style={{ margin: 0 }}>
          <Link
            to="/workspaces"
            style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}
          >
            {t('workspaces.back')}
          </Link>
        </p>
      </form>
    </AuthCard>
  );
}

export const Route = createFileRoute('/_authenticated/workspaces_/$wsId')({
  component: WorkspaceEditPage,
});
