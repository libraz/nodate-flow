/**
 * CalendarMembersTab — body of the Calendar Settings Drawer's
 * "Members" tab. Lists every member with avatar, role badge, an inline
 * role-change select, and a remove affordance. A bottom add-row
 * captures email + role and POSTs to `/calendars/{calId}/members`.
 *
 * The component enforces an "at least one owner" invariant on the
 * client side: when there's a single owner left, neither the role
 * select nor the remove button on that row is enabled. The backend
 * also rejects this state with a 409, so the guard is double-defense.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import { confirm } from '@nodate-flow/ui/primitives/confirm';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ChangeEvent, type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError } from '../../lib/api-error';
import styles from './calendar-members-tab.module.css';
import {
  type AddableRole,
  type CalendarMember,
  type UpdatableRole,
  useAddCalendarMemberMutation,
  useCalendarMembersQuery,
  useRemoveCalendarMemberMutation,
  useUpdateCalendarMemberRoleMutation,
} from './members-api';

const ROLE_TONE: Record<string, BadgeTone> = {
  owner: 'accent',
  manager: 'info',
  editor: 'success',
  viewer: 'neutral',
};

export interface CalendarMembersTabProps {
  workspaceId: string;
  calendarId: string;
}

export default function CalendarMembersTab({
  workspaceId,
  calendarId,
}: CalendarMembersTabProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: members } = useCalendarMembersQuery(workspaceId, calendarId);
  const updateRole = useUpdateCalendarMemberRoleMutation();
  const removeMember = useRemoveCalendarMemberMutation();
  const addMember = useAddCalendarMemberMutation();

  const [email, setEmail] = useState('');
  const [role, setRole] = useState<AddableRole>('editor');

  const ownerCount = members.filter((m) => m.role === 'owner').length;
  const isLastOwner = (member: CalendarMember): boolean =>
    member.role === 'owner' && ownerCount <= 1;

  const handleRoleChange = (member: CalendarMember, next: UpdatableRole): void => {
    if (next === member.role) return;
    if (member.role === 'owner' && isLastOwner(member)) {
      toaster.show({ tone: 'warning', message: t('calendar.settings.members.last_owner_block') });
      return;
    }
    updateRole.mutate(
      { wsId: workspaceId, calId: calendarId, userId: member.userId, role: next },
      {
        onError: (err) => {
          const message =
            err instanceof ApiError ? err.message : t('calendar.settings.members.update_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  const handleRemove = async (member: CalendarMember): Promise<void> => {
    if (isLastOwner(member)) {
      toaster.show({ tone: 'warning', message: t('calendar.settings.members.last_owner_block') });
      return;
    }
    const ok = await confirm.ask({
      title: t('calendar.settings.members.remove_confirm_title'),
      message: t('calendar.settings.members.remove_confirm', { name: member.displayName }),
      tone: 'danger',
      confirmLabel: t('calendar.settings.members.remove_confirm_action'),
      cancelLabel: t('calendar.settings.members.remove_cancel'),
    });
    if (!ok) return;
    removeMember.mutate(
      { wsId: workspaceId, calId: calendarId, userId: member.userId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('calendar.settings.members.remove_success') });
        },
        onError: (err) => {
          const message =
            err instanceof ApiError ? err.message : t('calendar.settings.members.remove_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  const handleAdd = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const trimmed = email.trim();
    if (trimmed.length === 0) return;
    addMember.mutate(
      { wsId: workspaceId, calId: calendarId, email: trimmed, role },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('calendar.settings.members.add_success') });
          setEmail('');
        },
        onError: (err) => {
          const message =
            err instanceof ApiError ? err.message : t('calendar.settings.members.add_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <div className={styles.body}>
      {members.length === 0 ? (
        <p className={styles.empty}>{t('calendar.settings.members.empty')}</p>
      ) : (
        <ul className={styles.list}>
          {members.map((member) => {
            const tone = ROLE_TONE[member.role] ?? 'neutral';
            const lockOwner = isLastOwner(member);
            return (
              <li key={member.userId} className={styles.row}>
                <Avatar
                  alt={member.displayName}
                  initials={member.displayName.slice(0, 2).toUpperCase()}
                  size="sm"
                  {...(member.avatarUrl ? { src: member.avatarUrl } : {})}
                />
                <div className={styles.identity}>
                  <span className={styles.displayName}>{member.displayName}</span>
                  <Badge tone={tone}>
                    {t(`calendar.settings.members.role.${member.role}` as const, {
                      defaultValue: member.role,
                    })}
                  </Badge>
                </div>
                <div className={styles.actions}>
                  <Select
                    value={member.role}
                    onChange={(e: ChangeEvent<HTMLSelectElement>) => {
                      handleRoleChange(member, e.target.value as UpdatableRole);
                    }}
                    aria-label={t('calendar.settings.members.role_select_label', {
                      name: member.displayName,
                    })}
                    disabled={lockOwner || updateRole.isPending}
                  >
                    <option value="owner">{t('calendar.settings.members.role.owner')}</option>
                    <option value="manager">{t('calendar.settings.members.role.manager')}</option>
                    <option value="editor">{t('calendar.settings.members.role.editor')}</option>
                    <option value="viewer">{t('calendar.settings.members.role.viewer')}</option>
                  </Select>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      void handleRemove(member);
                    }}
                    disabled={lockOwner || removeMember.isPending}
                  >
                    {t('calendar.settings.members.remove')}
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <form className={styles.addRow} onSubmit={handleAdd}>
        <FormField
          label={t('calendar.settings.members.add_email_label')}
          className={styles.addEmail}
        >
          {(control) => (
            <Input
              {...control}
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={t('calendar.settings.members.add_email_placeholder')}
              required
            />
          )}
        </FormField>
        <FormField label={t('calendar.settings.members.add_role_label')} className={styles.addRole}>
          {(control) => (
            <Select
              {...control}
              value={role}
              onChange={(e: ChangeEvent<HTMLSelectElement>) =>
                setRole(e.target.value as AddableRole)
              }
            >
              <option value="manager">{t('calendar.settings.members.role.manager')}</option>
              <option value="editor">{t('calendar.settings.members.role.editor')}</option>
              <option value="viewer">{t('calendar.settings.members.role.viewer')}</option>
            </Select>
          )}
        </FormField>
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={email.trim().length === 0 || addMember.isPending}
        >
          {t('calendar.settings.members.add')}
        </Button>
      </form>
    </div>
  );
}
