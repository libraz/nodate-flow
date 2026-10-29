/**
 * WorkspaceSettingsForm — edit name, slug, description, and icon URL.
 *
 * Suspense-ready: reads the workspace via `useWorkspaceQuery` (suspense mode);
 * {@link WorkspaceSettingsSkeleton} is the matching fallback. Validation is
 * performed inline (no Zod dependency in this small form). A successful save
 * makes the submitted values the new baseline, so the inline save status
 * tracks unsaved edits against what the server holds.
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
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import { clearActiveWorkspaceId } from '../../lib/use-current-workspace';
import { selectUser, useAuth } from '../auth/auth-store';
import SaveStatus, { useSaveStatus } from '../settings/save-status';
import ui from '../settings/settings-ui.module.css';
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

interface GeneralValues {
  name: string;
  slug: string;
  description: string;
  iconUrl: string;
}

function valuesOf(workspace: Workspace): GeneralValues {
  return {
    name: workspace.name,
    slug: workspace.slug,
    description: workspace.description ?? '',
    iconUrl: workspace.iconUrl ?? '',
  };
}

/** Suspense fallback shaped like the header, the general section and its footer. */
export function WorkspaceSettingsSkeleton(): ReactElement {
  return (
    <div className={ui.page} aria-busy="true">
      <div className={ui.headerText}>
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ blockSize: '2rem', inlineSize: '16rem' }} />
        {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
        <Skeleton style={{ blockSize: '1.25rem', inlineSize: '24rem', maxInlineSize: '100%' }} />
      </div>
      <div className={ui.section}>
        <div className={ui.skeletonStack}>
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '100%' }} />
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '100%' }} />
        </div>
        <div className={ui.actions}>
          {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
          <Skeleton style={{ blockSize: 'var(--nf-control-height)', inlineSize: '6rem' }} />
        </div>
      </div>
    </div>
  );
}

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
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
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
            gap: 'var(--nf-space-2)',
            padding: 'var(--nf-space-3)',
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
              paddingInlineStart: 'var(--nf-space-5)',
              fontSize: 'var(--nf-text-sm)',
              color: 'var(--nf-color-fg-muted)',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-1)',
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--nf-space-3)' }}>
          <Button type="button" variant="ghost" disabled={pending} onClick={onCancel}>
            {t('workspace.general.danger.confirm.cancel')}
          </Button>
          <Button
            type="submit"
            variant="danger"
            disabled={!matches || pending}
            aria-busy={pending || undefined}
          >
            {t('workspace.general.danger.confirm.submit')}
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

  // The values the server holds; the form is dirty while any field differs.
  const [baseline, setBaseline] = useState<GeneralValues>(() => valuesOf(workspace));
  const [name, setName] = useState(baseline.name);
  const [slug, setSlug] = useState(baseline.slug);
  const [description, setDescription] = useState(baseline.description);
  const [iconUrl, setIconUrl] = useState(baseline.iconUrl);
  const [submitting, setSubmitting] = useState(false);
  const dirty =
    name !== baseline.name ||
    slug !== baseline.slug ||
    description !== baseline.description ||
    iconUrl !== baseline.iconUrl;
  const saveStatus = useSaveStatus(dirty);
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
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.general.danger.errors.delete_failed'),
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
      // The trimmed values become both the fields and the new baseline.
      const saved: GeneralValues = {
        name: patch.name ?? '',
        slug: patch.slug ?? '',
        description: trimmedDesc,
        iconUrl: trimmedIcon,
      };
      setName(saved.name);
      setSlug(saved.slug);
      setDescription(saved.description);
      setIconUrl(saved.iconUrl);
      setBaseline(saved);
      saveStatus.markSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.general.errors.update_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('workspace.general.title')}</h1>
          <p className={ui.lede}>{t('workspace.general.description')}</p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        className={ui.section}
        noValidate
      >
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

        <div className={ui.actions}>
          <SaveStatus saved={saveStatus.saved} dirty={dirty} />
          <Button
            type="submit"
            variant="primary"
            disabled={submitting}
            aria-busy={submitting || undefined}
          >
            {t('workspace.general.save')}
          </Button>
        </div>
      </form>

      {isOwner ? (
        <section aria-labelledby="workspace-danger-zone-heading" className={ui.dangerZone}>
          <div className={ui.dangerRow}>
            <div className={ui.sectionHeader}>
              <h2 id="workspace-danger-zone-heading" className={ui.dangerTitle}>
                {t('workspace.general.danger.title')}
              </h2>
              <p className={ui.sectionDescription}>{t('workspace.general.danger.description')}</p>
            </div>
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
    </div>
  );
}
