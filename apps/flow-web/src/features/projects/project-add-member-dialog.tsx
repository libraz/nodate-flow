/**
 * ProjectAddMemberDialog — add a workspace member to a project by user id + role.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';

import { type ProjectRole, useAddProjectMember } from './api';

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
  const addMember = useAddProjectMember();

  const [userId, setUserId] = useState('');
  const [role, setRole] = useState<ProjectRole>('editor');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

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
    } catch {
      toaster.show({ tone: 'danger', message: t('projects.errors.add_member_failed') });
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
    <Dialog open={open} onClose={handleClose} title={t('projects.members.add')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
        <FormField
          label={t('projects.members.user_id')}
          required
          {...(errors.userId ? { error: t(errors.userId) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              value={userId}
              onChange={(e) => {
                setUserId(e.target.value);
              }}
              autoFocus
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('projects.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('projects.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
