/**
 * RoleChoice — workspace role picker shown as radio cards, each with a
 * one-line description of what the role can do, so the difference is
 * visible before choosing.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Radio from '@nodate-flow/ui/primitives/radio';
import { type ReactElement, useId } from 'react';
import { useTranslation } from 'react-i18next';

import styles from './workspaces.module.css';

export type WorkspaceRole = 'owner' | 'admin' | 'member' | 'guest';

export interface RoleChoiceProps<R extends WorkspaceRole> {
  roles: readonly R[];
  value: R;
  onChange: (role: R) => void;
  disabled?: boolean;
}

export default function RoleChoice<R extends WorkspaceRole>({
  roles,
  value,
  onChange,
  disabled,
}: RoleChoiceProps<R>): ReactElement {
  const { t } = useTranslation('common');
  const name = useId();

  const label = (r: WorkspaceRole): string => {
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
  const description = (r: WorkspaceRole): string => {
    switch (r) {
      case 'owner':
        return t('workspaces.role_desc.owner');
      case 'admin':
        return t('workspaces.role_desc.admin');
      case 'member':
        return t('workspaces.role_desc.member');
      case 'guest':
        return t('workspaces.role_desc.guest');
    }
  };

  return (
    <fieldset className={styles.roleFieldset} disabled={disabled}>
      <legend className={styles.roleLegend}>{t('workspaces.members.role')}</legend>
      <div className={styles.roleOptions}>
        {roles.map((r) => {
          const inputId = `${name}-${r}`;
          const descId = `${inputId}-desc`;
          return (
            <label
              key={r}
              htmlFor={inputId}
              className={cx(styles.roleOption, r === value && styles.roleOptionOn)}
            >
              <Radio
                id={inputId}
                name={name}
                value={r}
                checked={r === value}
                onChange={() => {
                  onChange(r);
                }}
                aria-describedby={descId}
                className={styles.roleRadio}
              />
              <span className={styles.roleText}>
                <span className={styles.roleName}>{label(r)}</span>
                <span id={descId} className={styles.roleDesc}>
                  {description(r)}
                </span>
              </span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}
