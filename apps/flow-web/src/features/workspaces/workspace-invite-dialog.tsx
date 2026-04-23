/**
 * WorkspaceInviteDialog — create a shareable invite link for a workspace.
 *
 * Two-step flow:
 * 1. Form: role, expiry, max uses, optional label
 * 2. Success: generated link with a copy button
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { CreateInviteInput } from './invite-api';
import { useCreateInvite } from './invite-api';

type Role = CreateInviteInput['role'];

const ROLES: readonly Role[] = ['admin', 'member', 'guest'];

interface ExpiryOption {
  labelKey: string;
  seconds: number | undefined;
}

const EXPIRY_OPTIONS: readonly ExpiryOption[] = [
  { labelKey: 'workspaces.invites.1_day', seconds: 86400 },
  { labelKey: 'workspaces.invites.7_days', seconds: 604800 },
  { labelKey: 'workspaces.invites.30_days', seconds: 2592000 },
  { labelKey: 'workspaces.invites.no_expiry', seconds: undefined },
];

interface MaxUsesOption {
  labelKey: string;
  value: number | undefined;
}

const MAX_USES_OPTIONS: readonly MaxUsesOption[] = [
  { labelKey: 'workspaces.invites.uses_1', value: 1 },
  { labelKey: 'workspaces.invites.uses_10', value: 10 },
  { labelKey: 'workspaces.invites.uses_25', value: 25 },
  { labelKey: 'workspaces.invites.unlimited', value: undefined },
];

export interface WorkspaceInviteDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

export default function WorkspaceInviteDialog({
  workspaceId,
  open,
  onClose,
}: WorkspaceInviteDialogProps): ReactElement {
  const { t } = useTranslation('common');
  const createInvite = useCreateInvite();

  const [role, setRole] = useState<Role>('member');
  const [expiryIndex, setExpiryIndex] = useState(1); // default 7 days
  const [maxUsesIndex, setMaxUsesIndex] = useState(1); // default 10
  const [label, setLabel] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [generatedUrl, setGeneratedUrl] = useState<string | null>(null);

  const reset = (): void => {
    setRole('member');
    setExpiryIndex(1);
    setMaxUsesIndex(1);
    setLabel('');
    setGeneratedUrl(null);
  };

  const handleClose = (): void => {
    if (submitting) return;
    reset();
    onClose();
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setSubmitting(true);
    try {
      const expiry = EXPIRY_OPTIONS[expiryIndex];
      const maxUses = MAX_USES_OPTIONS[maxUsesIndex];
      const input: CreateInviteInput = {
        role,
        ...(expiry?.seconds != null ? { expiresIn: expiry.seconds } : {}),
        ...(maxUses?.value != null ? { maxUses: maxUses.value } : {}),
        ...(label.trim() ? { label: label.trim() } : {}),
      };
      const result = await createInvite.mutateAsync({ wsId: workspaceId, input });
      const url = `${globalThis.location.origin}/invite/${result.token}`;
      setGeneratedUrl(url);
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.invites.create_failed') });
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = async (): Promise<void> => {
    if (!generatedUrl) return;
    try {
      await navigator.clipboard.writeText(generatedUrl);
      toaster.show({ tone: 'success', message: t('workspaces.invites.copied') });
    } catch {
      toaster.show({ tone: 'danger', message: t('workspaces.invites.copy_failed') });
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

  if (generatedUrl) {
    return (
      <Dialog open={open} onClose={handleClose} title={t('workspaces.invites.link_ready_title')}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted, var(--nf-color-fg-muted))' }}>
            {t('workspaces.invites.link_ready_description')}
          </p>
          <div
            style={{
              display: 'flex',
              gap: '0.5rem',
              alignItems: 'center',
            }}
          >
            <Input
              readOnly
              value={generatedUrl}
              style={{ flex: 1 }}
              onFocus={(e) => {
                e.target.select();
              }}
            />
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                void handleCopy();
              }}
            >
              {t('workspaces.invites.copy')}
            </Button>
          </div>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Button variant="ghost" onClick={handleClose}>
              {t('workspaces.form.close')}
            </Button>
          </div>
        </div>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onClose={handleClose} title={t('workspaces.invites.create_title')}>
      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        noValidate
        style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
      >
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

        <FormField label={t('workspaces.invites.expires')}>
          {(control) => (
            <Select
              {...control}
              value={String(expiryIndex)}
              onChange={(e) => {
                setExpiryIndex(Number(e.target.value));
              }}
            >
              {EXPIRY_OPTIONS.map((opt, i) => (
                <option key={opt.labelKey} value={String(i)}>
                  {t(opt.labelKey)}
                </option>
              ))}
            </Select>
          )}
        </FormField>

        <FormField label={t('workspaces.invites.max_uses')}>
          {(control) => (
            <Select
              {...control}
              value={String(maxUsesIndex)}
              onChange={(e) => {
                setMaxUsesIndex(Number(e.target.value));
              }}
            >
              {MAX_USES_OPTIONS.map((opt, i) => (
                <option key={opt.labelKey} value={String(i)}>
                  {t(opt.labelKey)}
                </option>
              ))}
            </Select>
          )}
        </FormField>

        <FormField label={t('workspaces.invites.label')}>
          {(control) => (
            <Input
              {...control}
              value={label}
              onChange={(e) => {
                setLabel(e.target.value);
              }}
              placeholder={t('workspaces.invites.label_placeholder')}
            />
          )}
        </FormField>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.75rem' }}>
          <Button type="button" variant="ghost" onClick={handleClose} disabled={submitting}>
            {t('workspaces.form.cancel')}
          </Button>
          <Button type="submit" variant="primary" disabled={submitting}>
            {t('workspaces.invites.create')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
