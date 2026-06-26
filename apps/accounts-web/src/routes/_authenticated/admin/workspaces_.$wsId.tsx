/**
 * /admin/workspaces/$wsId -- Workspace detail with suspend/enable and a
 * destructive "delete workspace" action.
 *
 * Deletion is a single-step destructive call — there is no preceding
 * suspend requirement. The danger-zone block at the bottom of the page
 * gates the operation behind a typed-confirmation modal that requires
 * the operator to type the workspace slug verbatim.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { confirmAction } from '@nodate-flow/ui/primitives/confirm/action';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { adminBadgeBase, adminLabelStyle, adminValueStyle } from '../../../features/admin/styles';
import type { ProblemJson } from '../../../lib/api-error';
import { extractErrorCode } from '../../../lib/auth-errors';
import { formatTimestamp } from '../../../lib/format-timestamp';
import { sdk } from '../../../lib/sdk';
import { useSubmitGuard } from '../../../lib/use-submit-guard';

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
              paddingInlineStart: '1.25rem',
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
          <Button type="submit" variant="danger" disabled={!matches || pending}>
            {pending ? t('workspaces.danger.deleting') : t('workspaces.danger.confirm.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function WorkspaceDetailPage(): ReactElement {
  const { wsId } = Route.useParams();
  const { t } = useTranslation('admin');
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

    void sdk.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } }).then((result) => {
      if (cancelled) return;
      if (result.error || !result.data) {
        setError(t('errors.generic'));
        setLoading(false);
        return;
      }
      setWorkspace(result.data as WorkspaceDetail);
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
    await sdk.PATCH('/admin/workspaces/{wsId}', {
      params: { path: { wsId } },
      body: { enabled: !workspace.enabled },
    });
    setActionLoading(false);

    // Refetch
    const result = await sdk.GET('/admin/workspaces/{wsId}', { params: { path: { wsId } } });
    if (result.data) {
      setWorkspace(result.data as WorkspaceDetail);
    }
  };

  const handleDeleteConfirmed = async (): Promise<void> => {
    if (!workspace) return;
    if (deleteGuard.guard()) return;
    try {
      const { error: err } = await sdk.DELETE('/admin/workspaces/{wsId}', {
        params: { path: { wsId } },
        body: { confirm: true },
      });
      if (err) {
        const code = extractErrorCode(err as ProblemJson);
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

  if (loading) {
    return <p style={{ color: 'var(--nf-color-fg-muted)' }}>{t('common.loading')}</p>;
  }

  if (error || !workspace) {
    return (
      <div>
        <Link to="/admin/workspaces" style={{ color: 'var(--nf-color-accent)' }}>
          {t('common.back_to_workspaces')}
        </Link>
        <p
          role="alert"
          style={{
            color: 'var(--nf-color-danger)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {error ?? t('errors.generic')}
        </p>
      </div>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6)' }}>
      <div>
        <Link
          to="/admin/workspaces"
          style={{
            color: 'var(--nf-color-accent)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('common.back_to_workspaces')}
        </Link>
      </div>

      <h1
        style={{
          fontFamily: 'var(--nf-font-sans)',
          fontSize: 'var(--nf-text-2xl)',
          margin: 0,
        }}
      >
        {t('workspaces.detail')}
      </h1>

      <div
        style={{
          padding: 'var(--nf-space-4)',
          border: '1px solid var(--nf-color-border)',
          borderRadius: 'var(--nf-radius-md)',
        }}
      >
        <div style={adminLabelStyle}>{t('workspaces.name')}</div>
        <div style={adminValueStyle}>{workspace.name}</div>

        <div style={adminLabelStyle}>{t('workspaces.slug')}</div>
        <div style={adminValueStyle}>{workspace.slug}</div>

        <div style={adminLabelStyle}>{t('workspaces.members')}</div>
        <div style={adminValueStyle}>{workspace.memberCount}</div>

        <div style={adminLabelStyle}>{t('workspaces.projects')}</div>
        <div style={adminValueStyle}>{workspace.projectCount}</div>

        <div style={adminLabelStyle}>{t('workspaces.status')}</div>
        <div style={adminValueStyle}>
          <span
            style={{
              ...adminBadgeBase,
              background: workspace.enabled
                ? 'color-mix(in srgb, var(--nf-color-success) 15%, transparent)'
                : 'color-mix(in srgb, var(--nf-color-danger) 15%, transparent)',
              color: workspace.enabled ? 'var(--nf-color-success)' : 'var(--nf-color-danger)',
            }}
          >
            {workspace.enabled ? t('workspaces.enabled') : t('workspaces.disabled')}
          </span>
        </div>

        <div style={adminLabelStyle}>{t('workspaces.created_at')}</div>
        <div style={adminValueStyle}>{formatTimestamp(workspace.createdAt, t('common.never'))}</div>
      </div>

      <div>
        <Button
          variant="default"
          disabled={actionLoading}
          onClick={() => void handleToggleEnabled()}
        >
          {workspace.enabled ? t('workspaces.suspend') : t('workspaces.enable')}
        </Button>
      </div>

      <section
        aria-labelledby="workspace-danger-zone-heading"
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-3)',
          marginBlockStart: 'var(--nf-space-4)',
          padding: 'var(--nf-space-4)',
          border: '1px solid var(--nf-color-danger)',
          borderRadius: 'var(--nf-radius-md)',
          background: 'color-mix(in srgb, var(--nf-color-danger) 4%, var(--nf-color-bg-default))',
        }}
      >
        <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          <h2
            id="workspace-danger-zone-heading"
            style={{
              margin: 0,
              fontFamily: 'var(--nf-font-sans)',
              fontSize: 'var(--nf-text-lg)',
              color: 'var(--nf-color-danger)',
            }}
          >
            {t('workspaces.danger.title')}
          </h2>
          <p
            style={{
              margin: 0,
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-sm)',
            }}
          >
            {t('workspaces.danger.description')}
          </p>
        </header>
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
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
        </div>
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
      </section>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/workspaces_/$wsId')({
  component: WorkspaceDetailPage,
});
