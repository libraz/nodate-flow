/**
 * ProjectAddMemberDialog — add a workspace member to a project by picking
 * them from a searchable combobox (workspace member pool) + role.
 *
 * The wire shape is unchanged (`POST /projects/{id}/members` with
 * `{ userId, role }`); only the input control changes. Workspace members
 * already on the project are filtered out of the picker.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Combobox from '@nodate-flow/ui/primitives/combobox';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, Suspense, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { formatApiError } from '../../lib/api-error';
import { useWorkspaceMembersQuery } from '../workspaces/api';
import {
  type ProjectRole,
  useAddProjectMember,
  useProjectMembersQuery,
  useProjectQuery,
} from './api';

const ROLES: readonly ProjectRole[] = ['lead', 'editor', 'commenter', 'viewer'];

export interface ProjectAddMemberDialogProps {
  projectId: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  userId?: string;
}

const schema = z.object({
  userId: z.string().min(1, 'projects.validation.user_id_required'),
});

export default function ProjectAddMemberDialog({
  projectId,
  open,
  onClose,
}: ProjectAddMemberDialogProps): ReactElement {
  const { t } = useTranslation('common');

  return (
    <Dialog open={open} onClose={onClose} title={t('projects.members.add')}>
      <Suspense fallback={null}>
        <ProjectAddMemberDialogBody projectId={projectId} onClose={onClose} />
      </Suspense>
    </Dialog>
  );
}

interface ProjectAddMemberDialogBodyProps {
  projectId: string;
  onClose: () => void;
}

function ProjectAddMemberDialogBody({
  projectId,
  onClose,
}: ProjectAddMemberDialogBodyProps): ReactElement {
  const { t } = useTranslation('common');
  const addMember = useAddProjectMember();

  const { data: project } = useProjectQuery(projectId);
  const { data: workspaceMembers } = useWorkspaceMembersQuery(project.workspaceId);
  const { data: projectMembers } = useProjectMembersQuery(projectId);

  const [userId, setUserId] = useState('');
  const [role, setRole] = useState<ProjectRole>('editor');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const availableOptions = useMemo(() => {
    const existing = new Set(projectMembers.map((m) => m.userId));
    return workspaceMembers
      .filter((m) => !existing.has(m.userId))
      .map((m) => ({ value: m.userId, label: `${m.displayName} (${m.email})` }));
  }, [workspaceMembers, projectMembers]);

  const reset = (): void => {
    setUserId('');
    setRole('editor');
    setErrors({});
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const parsed = schema.safeParse({ userId });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        if (issue.path[0] === 'userId') next.userId = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      await addMember.mutateAsync({
        id: projectId,
        input: { userId: parsed.data.userId, role },
      });
      reset();
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'projects.errors.add_member_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const roleLabel = (r: ProjectRole): string => {
    switch (r) {
      case 'lead':
        return t('projects.roles.lead');
      case 'editor':
        return t('projects.roles.editor');
      case 'commenter':
        return t('projects.roles.commenter');
      case 'viewer':
        return t('projects.roles.viewer');
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      noValidate
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
    >
      <FormField
        label={t('projects.members.user_id')}
        required
        {...(errors.userId ? { error: t(errors.userId) } : {})}
      >
        {(control) => (
          <Combobox
            id={control.id}
            aria-label={t('projects.members.user_id')}
            placeholder={t('projects.members.add')}
            options={availableOptions}
            value={userId}
            onChange={(v) => {
              setUserId(v);
            }}
          />
        )}
      </FormField>

      <FormField label={t('projects.members.role')}>
        {(control) => (
          <Select
            {...control}
            value={role}
            onChange={(e) => {
              setRole(e.target.value as ProjectRole);
            }}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {roleLabel(r)}
              </option>
            ))}
          </Select>
        )}
      </FormField>

      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--nf-space-3)' }}>
        <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
          {t('projects.form.cancel')}
        </Button>
        <Button type="submit" variant="primary" disabled={submitting}>
          {t('projects.form.submit')}
        </Button>
      </div>
    </form>
  );
}
