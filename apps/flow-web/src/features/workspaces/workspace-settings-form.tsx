/**
 * WorkspaceSettingsForm — edit name, slug, description, and icon URL.
 *
 * Suspense-ready: reads the workspace via `useWorkspaceQuery` (suspense mode).
 * Validation is performed inline (no Zod dependency in this small form).
 *
 * Owner-only Danger zone renders below the main form. Deletion is a single
 * destructive step (no two-leg soft-delete-then-purge flow): the API call
 * sweeps every MinIO blob owned by the workspace and CASCADE-deletes the
 * row. The modal requires the operator to type the workspace slug before
 * the destructive button activates, mirroring industry-standard
 * "type-the-name-to-confirm" UX.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, Fragment, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { clearActiveWorkspaceId } from '../../lib/use-current-workspace';
import { selectUser, useAuth } from '../auth/auth-store';
import {
  type PatchWorkspaceInput,
  useDeleteWorkspace,
  useUpdateWorkspace,
  useWorkspaceMembersQuery,
  useWorkspaceQuery,
  type Workspace,
} from './api';

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

/**
 * DeleteWorkspaceDialog — modal with typed-confirmation gate.
 *
 * The destructive button stays disabled until the user types the
 * workspace slug exactly. The slug is stable (cannot be edited inside
 * the dialog), so this is purely a friction gate, not a validation.
 *
 * Internal to this file because it is purely presentational and binds
 * tightly to the workspace settings flow; pulling it into a shared
 * primitive would be premature abstraction.
 */
function DeleteWorkspaceDialog({
  open,
  workspace,
  memberCount,
  pending,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  workspace: Workspace;
  memberCount: number;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}): ReactElement {
  const { t } = useTranslation('settings');
  const [typed, setTyped] = useState('');
  const matches = typed.trim() === workspace.slug;

  // Reset the typed value whenever the dialog re-opens. A render-time
  // guard avoids a useEffect dance: if `open` flipped from false→true
  // since the last render and the typed buffer still holds the prior
  // value, clear it. The captured `prevOpen` is stored alongside the
  // input value so this stays render-driven and never triggers a
  // double commit.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setTyped('');
  }

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!matches || pending) return;
    onConfirm();
  };

  return (
    <Dialog
      open={open}
      onClose={onCancel}
      size="md"
      title={t('workspace.general.danger.confirm.title', { name: workspace.name })}
      dismissOnOverlayClick={!pending}
    >
      <form
        onSubmit={handleSubmit}
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
        <p
          style={{
            margin: 0,
            color: 'var(--nf-color-fg-muted)',
            fontSize: 'var(--nf-text-sm)',
            lineHeight: 1.5,
          }}
        >
          {t('workspace.general.danger.confirm.warning')}
        </p>

        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '0.5rem',
            padding: '0.75rem',
            border: '1px solid var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          <strong style={{ fontSize: 'var(--nf-text-sm)' }}>
            {t('workspace.general.danger.confirm.loses_heading')}
          </strong>
          <ul
            style={{
              margin: 0,
              paddingInlineStart: '1.25rem',
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg-muted)',
              display: 'flex',
              flexDirection: 'column',
              gap: '0.25rem',
            }}
          >
            <li>{t('workspace.general.danger.confirm.loses_projects_and_tasks')}</li>
            <li>{t('workspace.general.danger.confirm.loses_members', { count: memberCount })}</li>
            <li>{t('workspace.general.danger.confirm.loses_attachments')}</li>
          </ul>
        </div>

        <FormField
          label={t('workspace.general.danger.confirm.type_to_confirm_label')}
          description={t('workspace.general.danger.confirm.type_to_confirm_help', {
            slug: workspace.slug,
          })}
        >
          {(control) => (
            <Input
              {...control}
              autoComplete="off"
              spellCheck={false}
              dir="ltr"
              value={typed}
              placeholder={t('workspace.general.danger.confirm.type_to_confirm_placeholder')}
              onChange={(e) => {
                setTyped(e.target.value);
              }}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" disabled={pending} onClick={onCancel}>
            {t('workspace.general.danger.confirm.cancel')}
          </Button>
          <Button type="submit" variant="danger" disabled={!matches || pending}>
            {pending
              ? t('workspace.general.danger.deleting')
              : t('workspace.general.danger.confirm.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export default function WorkspaceSettingsForm({
  workspaceId,
}: WorkspaceSettingsFormProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: workspace } = useWorkspaceQuery(workspaceId);
  const { data: members } = useWorkspaceMembersQuery(workspaceId);
  const currentUser = useAuth(selectUser);
  const update = useUpdateWorkspace();
  const deleteWs = useDeleteWorkspace();
  const navigate = useNavigate();

  const currentMember = members.find((m) => m.userId === currentUser?.id);
  const isOwner = currentMember?.role === 'owner';

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
  const [deleteOpen, setDeleteOpen] = useState(false);

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

  const handleConfirmDelete = async (): Promise<void> => {
    // Navigate before awaiting the mutation so suspense consumers of
    // the workspace detail (e.g. <WorkspaceBreadcrumb>) unmount before
    // the DELETE lands and its onSuccess removes the cached data.
    // Otherwise the consumer re-suspends, refetches, and throws a 404
    // into the nearest ErrorBoundary during the brief navigation
    // transition.
    void navigate({ to: '/workspaces' });
    clearActiveWorkspaceId();
    setDeleteOpen(false);
    try {
      await deleteWs.mutateAsync({ wsId: workspaceId, confirm: true });
      toaster.show({ tone: 'success', message: t('workspace.general.danger.deleted') });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.general.danger.errors.delete_failed'),
      });
    }
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
    <Fragment>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
        <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
          <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>
            {t('workspace.general.title')}
          </h1>
          <p
            style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}
          >
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

      {isOwner ? (
        <section
          aria-labelledby="workspace-danger-zone-heading"
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '1rem',
            marginBlockStart: '2rem',
            padding: '1.25rem',
            border: '1px solid var(--nf-color-danger)',
            borderRadius: 'var(--nf-radius-md)',
            background: 'color-mix(in srgb, var(--nf-color-danger) 4%, var(--nf-color-bg-default))',
          }}
        >
          <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
            <h2
              id="workspace-danger-zone-heading"
              style={{
                margin: 0,
                fontSize: 'var(--nf-text-xl)',
                color: 'var(--nf-color-danger)',
              }}
            >
              {t('workspace.general.danger.title')}
            </h2>
            <p
              style={{
                margin: 0,
                color: 'var(--nf-color-fg-muted)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              {t('workspace.general.danger.description')}
            </p>
          </header>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Button
              type="button"
              variant="danger"
              disabled={deleteWs.isPending}
              onClick={() => {
                setDeleteOpen(true);
              }}
            >
              {t('workspace.general.danger.delete')}
            </Button>
          </div>
          <DeleteWorkspaceDialog
            open={deleteOpen}
            workspace={workspace}
            memberCount={members.length}
            pending={deleteWs.isPending}
            onCancel={() => {
              if (!deleteWs.isPending) setDeleteOpen(false);
            }}
            onConfirm={() => {
              void handleConfirmDelete();
            }}
          />
        </section>
      ) : null}
    </Fragment>
  );
}
