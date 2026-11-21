/**
 * /admin/workspaces/$wsId -- Workspace detail with suspend/enable and a
 * destructive "delete workspace" action.
 *
 * Deletion is a single-step destructive call — there is no preceding
 * suspend requirement. The danger-zone block at the bottom of the page
 * gates the operation behind a typed-confirmation modal that requires
 * the operator to type the workspace slug verbatim.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button, { buttonClassName } from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../../../components/page-header';
import SectionCard, { SectionActions } from '../../../components/section-card';
import { apiRequest } from '../../../lib/api';
import { refusalCode } from '../../../lib/auth-errors';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { useSubmitGuard } from '../../../lib/use-submit-guard';
import styles from './workspaces-detail.module.css';

interface WorkspaceDetail {
  id: string;
  name: string;
  slug: string;
  memberCount: number;
  projectCount: number;
  enabled: boolean;
  createdAt: number;
}

/**
 * DeleteWorkspaceDialog — destructive-delete modal with typed-confirmation.
 * Internal to this file because it binds tightly to the admin
 * workspace-detail page; the dialog requires the operator to type the
 * workspace slug verbatim before the destructive button activates.
 */
function DeleteWorkspaceDialog({
  open,
  workspace,
  pending,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  workspace: WorkspaceDetail;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}): ReactElement {
  const { t } = useTranslation('admin');
  const [typed, setTyped] = useState('');
  const matches = typed.trim() === workspace.slug;

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
      title={t('workspaces.danger.confirm.title', { name: workspace.name })}
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
          {t('workspaces.danger.confirm.warning')}
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
            {t('workspaces.danger.confirm.loses_heading')}
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
            <li>{t('workspaces.danger.confirm.loses_projects_and_tasks')}</li>
            <li>
              {t('workspaces.danger.confirm.loses_members', { count: workspace.memberCount })}
            </li>
            <li>{t('workspaces.danger.confirm.loses_attachments')}</li>
          </ul>
        </div>

        <FormField
          label={t('workspaces.danger.confirm.type_to_confirm_label')}
          description={t('workspaces.danger.confirm.type_to_confirm_help', {
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
              placeholder={t('workspaces.danger.confirm.type_to_confirm_placeholder')}
              onChange={(e) => {
                setTyped(e.target.value);
              }}
            />
          )}
        </FormField>

        <div
          style={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--nf-space-3)',
          }}
        >
          <Button type="button" variant="ghost" disabled={pending} onClick={onCancel}>
            {t('workspaces.danger.confirm.cancel')}
          </Button>
          <Button
            type="submit"
            variant="danger"
            aria-busy={pending || undefined}
            disabled={!matches || pending}
          >
            {t('workspaces.danger.confirm.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function WorkspaceDetailPage(): ReactElement {
  const { wsId } = Route.useParams();
  const { t, i18n } = useTranslation('admin');
  const locale = i18n.resolvedLanguage ?? 'en';
  const navigate = useNavigate();
  const [workspace, setWorkspace] = useState<WorkspaceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteGuard = useSubmitGuard();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    void apiRequest(
      (client) => client.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } }),
      'Failed to load the workspace',
      { onError: 'empty', empty: null },
    ).then((result) => {
      if (cancelled) return;
      if (result === null) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setWorkspace(result as WorkspaceDetail);
      setLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [wsId, t]);

  const handleToggleEnabled = async () => {
    if (!workspace) return;
    const ok = await confirmAction({
      tone: 'danger',
      message: workspace.enabled ? t('workspaces.confirm_suspend') : t('workspaces.confirm_enable'),
      confirmLabel: workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable'),
    });
    if (!ok) return;

    setActionLoading(true);
    try {
      await apiRequest(
        (client) =>
          client.PATCH('/admin/workspaces/{wsId}', {
            params: { path: { wsId } },
            body: { enabled: !workspace.enabled },
          }),
        'Failed to update the workspace',
      );
    } catch (err) {
      const code = refusalCode(err);
      toaster.show({
        tone: 'danger',
        message: code ? `${t('errors.generic')} (${code})` : t('errors.generic'),
      });
      return;
    } finally {
      setActionLoading(false);
    }

    // Refetch
    const result = await apiRequest(
      (client) => client.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } }),
      'Failed to load the workspace',
      { onError: 'empty', empty: null },
    );
    if (result) {
      setWorkspace(result as WorkspaceDetail);
    }
  };

  const handleDeleteConfirmed = async (): Promise<void> => {
    if (!workspace) return;
    if (deleteGuard.guard()) return;
    try {
      try {
        await apiRequest(
          (client) =>
            client.DELETE('/admin/workspaces/{wsId}', {
              params: { path: { wsId } },
              body: { confirm: true },
            }),
          'Failed to delete the workspace',
        );
      } catch (err) {
        const code = refusalCode(err);
        toaster.show({
          tone: 'danger',
          message: code
            ? t('workspaces.danger.errors.delete_failed_with_code', { code })
            : t('workspaces.danger.errors.delete_failed'),
        });
        return;
      }
      setDeleteOpen(false);
      toaster.show({ tone: 'success', message: t('workspaces.danger.deleted') });
      void navigate({ to: '/admin/workspaces' });
    } finally {
      deleteGuard.end();
    }
  };

  const backLink = (
    <Link
      to="/admin/workspaces"
      className={buttonClassName({ variant: 'ghost', size: 'sm', className: styles.back })}
    >
      {t('common.back_to_workspaces')}
    </Link>
  );

  if (loading) {
    return (
      <>
        {backLink}
        <PageHeader title={t('workspaces.detail')} />
        <div className="aw-stack aw-stack-6" aria-busy="true">
          <VisuallyHidden>{t('common.loading')}</VisuallyHidden>
          <SectionCard>
            <Skeleton />
            <Skeleton />
            <Skeleton />
          </SectionCard>
        </div>
      </>
    );
  }

  if (error || !workspace) {
    return (
      <>
        {backLink}
        <PageHeader title={t('workspaces.detail')} />
        <p role="alert" className="aw-error">
          {error ?? t('errors.generic')}
        </p>
      </>
    );
  }

  return (
    <>
      {backLink}
      <PageHeader title={t('workspaces.detail')} description={workspace.name} />
      <div className="aw-stack aw-stack-6">
        <SectionCard>
          <dl className="aw-dl">
            <dt>{t('workspaces.name')}</dt>
            <dd>{workspace.name}</dd>

            <dt>{t('workspaces.slug')}</dt>
            <dd>{workspace.slug}</dd>

            <dt>{t('workspaces.members')}</dt>
            <dd>{workspace.memberCount}</dd>

            <dt>{t('workspaces.projects')}</dt>
            <dd>{workspace.projectCount}</dd>

            <dt>{t('workspaces.status')}</dt>
            <dd>
              <Badge tone={workspace.enabled ? 'success' : 'danger'}>
                {workspace.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
              </Badge>
            </dd>

            <dt>{t('workspaces.created_at')}</dt>
            <dd>{formatTimestamp(workspace.createdAt, { locale, fallback: t('common.never') })}</dd>
          </dl>

          <SectionActions>
            <Button
              variant="default"
              aria-busy={actionLoading || undefined}
              disabled={actionLoading}
              onClick={() => void handleToggleEnabled()}
            >
              {workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable')}
            </Button>
          </SectionActions>
        </SectionCard>

        <SectionCard
          tone="danger"
          title={t('workspaces.danger.title')}
          description={t('workspaces.danger.description')}
        >
          <SectionActions>
            <Button
              type="button"
              variant="danger"
              disabled={deleteGuard.submitting}
              onClick={() => {
                setDeleteOpen(true);
              }}
            >
              {t('workspaces.danger.delete')}
            </Button>
          </SectionActions>
          <DeleteWorkspaceDialog
            open={deleteOpen}
            workspace={workspace}
            pending={deleteGuard.submitting}
            onCancel={() => {
              if (!deleteGuard.submitting) setDeleteOpen(false);
            }}
            onConfirm={() => {
              void handleDeleteConfirmed();
            }}
          />
        </SectionCard>
      </div>
    </>
  );
}

export const Route = createFileRoute('/_authenticated/admin/workspaces_/$wsId')({
  component: WorkspaceDetailPage,
});
