/**
 * WorkspaceAddMemberDialog — grant a workspace seat by email + role.
 *
 * This adds the member outright: nothing is mailed and the address is
 * never asked to accept. Use {@link WorkspaceInviteDialog} for a link
 * the recipient redeems themselves.
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

import { formatApiError } from '../../lib/api-error';
import { type AddMemberInput, useAddMember } from './api';

type Role = AddMemberInput['role'];

const ROLES: readonly Role[] = ['owner', 'admin', 'member', 'guest'];

export interface WorkspaceAddMemberDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

interface FieldErrors {
  email?: string;
}

const schema = z.object({
  email: z
    .string()
    .min(1, 'workspaces.validation.email_required')
    .email('workspaces.validation.email_invalid'),
});

export default function WorkspaceAddMemberDialog({
  workspaceId,
  open,
  onClose,
}: WorkspaceAddMemberDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const addMember = useAddMember();

  const [email, setEmail] = useState('');
  const [role, setRole] = useState<Role>('member');
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitting, setSubmitting] = useState(false);

  const reset = (): void => {
    setEmail('');
    setRole('member');
    setErrors({});
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const parsed = schema.safeParse({ email });
    if (!parsed.success) {
      const next: FieldErrors = {};
      for (const issue of parsed.error.issues) {
        if (issue.path[0] === 'email') next.email = issue.message;
      }
      setErrors(next);
      return;
    }
    setErrors({});
    setSubmitting(true);
    try {
      await addMember.mutateAsync({
        id: workspaceId,
        input: { email: parsed.data.email, role },
      });
      reset();
      onClose();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspaces.errors.add_member_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const roleLabel = (r: Role): string => {
    switch (r) {
      case 'owner':
        return t('workspaces.roles.owner');
      case 'admin':
        return t('workspaces.roles.admin');
      case 'member':
        return t('workspaces.roles.member');
      case 'guest':
        return t('workspaces.roles.guest');
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('workspaces.members.add')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-5)' }}
      >
        <FormField
          label={t('workspaces.members.email')}
          required
          {...(errors.email ? { error: t(errors.email) } : {})}
        >
          {(control) => (
            <Input
              {...control}
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
              }}
              autoFocus
            />
          )}
        </FormField>

        <FormField label={t('workspaces.members.role')}>
          {(control) => (
            <Select
              {...control}
              value={role}
              onChange={(e) => {
                setRole(e.target.value as Role);
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
            {t('workspaces.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('workspaces.form.submit')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
