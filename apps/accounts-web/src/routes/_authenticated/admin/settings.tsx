/**
 * /admin/settings -- Instance settings management.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { ProblemJson } from '../../../lib/api-error';
import { extractErrorCode } from '../../../lib/auth-errors';
import { sdk } from '../../../lib/sdk';
import styles from './settings.module.css';

/**
 * SDK-derived types so the key/value rows stay in lock-step with the
 * Go schema even when fields like `updatedAt` get added server-side.
 */
type SettingsResponse = components['schemas']['ListSettingsOutputBody'];

const settingKeys = {
  registrationOpen: 'registration_open',
  mfaEnforcement: 'mfa_enforcement',
  maxWorkspacesPerUser: 'max_workspaces_per_user',
  maxMembersPerWorkspace: 'max_members_per_workspace',
} as const;

export function SettingsPage(): ReactElement {
  const { t } = useTranslation('admin');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const [registrationOpen, setRegistrationOpen] = useState('true');
  const [mfaEnforcement, setMfaEnforcement] = useState('none');
  const [maxWorkspacesPerUser, setMaxWorkspacesPerUser] = useState('');
  const [maxMembersPerWorkspace, setMaxMembersPerWorkspace] = useState('');

  useEffect(() => {
    void sdk.GET('/admin/settings').then((result) => {
      if (result.data) {
        const body = result.data as SettingsResponse;
        for (const s of body.items ?? []) {
          switch (s.key) {
            case 'registration_open':
              setRegistrationOpen(s.value);
              break;
            case 'mfa_enforcement':
              setMfaEnforcement(s.value);
              break;
            case 'max_workspaces_per_user':
              setMaxWorkspacesPerUser(s.value);
              break;
            case 'max_members_per_workspace':
              setMaxMembersPerWorkspace(s.value);
              break;
          }
        }
      }
      setLoading(false);
    });
  }, []);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    setSuccess(false);

    const settings: Record<string, string> = {
      [settingKeys.registrationOpen]: registrationOpen,
      [settingKeys.mfaEnforcement]: mfaEnforcement,
    };
    if (maxWorkspacesPerUser) {
      settings[settingKeys.maxWorkspacesPerUser] = maxWorkspacesPerUser;
    }
    if (maxMembersPerWorkspace) {
      settings[settingKeys.maxMembersPerWorkspace] = maxMembersPerWorkspace;
    }

    const { error: err } = await sdk.PATCH('/admin/settings', {
      body: { settings },
    });
    setSaving(false);

    if (err) {
      const code = extractErrorCode(err as ProblemJson);
      setError(code ? `${t('errors.generic')} (${code})` : t('errors.generic'));
    } else {
      setSuccess(true);
    }
  };

  if (loading) {
    return <p className={styles.loading}>{t('common.loading')}</p>;
  }

  return (
    <div className={styles.page}>
      <h1 className={styles.title}>{t('settings.title')}</h1>

      <div className={styles.field}>
        <label className={styles.label}>
          {t('settings.registration_open')}
          <p className={styles.description}>{t('settings.registration_open_desc')}</p>
          <select
            value={registrationOpen}
            onChange={(e) => setRegistrationOpen(e.target.value)}
            className={styles.input}
          >
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        </label>
      </div>

      <div className={styles.field}>
        <label className={styles.label}>
          {t('settings.mfa_enforcement')}
          <p className={styles.description}>{t('settings.mfa_enforcement_desc')}</p>
          <select
            value={mfaEnforcement}
            onChange={(e) => setMfaEnforcement(e.target.value)}
            className={styles.input}
          >
            <option value="none">{t('settings.mfa_none')}</option>
            <option value="optional">{t('settings.mfa_optional')}</option>
            <option value="required">{t('settings.mfa_required')}</option>
          </select>
        </label>
      </div>

      <div className={styles.field}>
        <label className={styles.label}>
          {t('settings.max_workspaces_per_user')}
          <input
            type="number"
            min="0"
            value={maxWorkspacesPerUser}
            onChange={(e) => setMaxWorkspacesPerUser(e.target.value)}
            className={styles.input}
            placeholder={t('settings.unlimited_placeholder')}
          />
        </label>
      </div>

      <div className={styles.field}>
        <label className={styles.label}>
          {t('settings.max_members_per_workspace')}
          <input
            type="number"
            min="0"
            value={maxMembersPerWorkspace}
            onChange={(e) => setMaxMembersPerWorkspace(e.target.value)}
            className={styles.input}
            placeholder={t('settings.unlimited_placeholder')}
          />
        </label>
      </div>

      {error ? (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      ) : null}

      {success ? <output className={styles.success}>{t('settings.saved')}</output> : null}

      <div>
        <Button variant="primary" disabled={saving} onClick={() => void handleSave()}>
          {saving ? t('settings.saving') : t('settings.save')}
        </Button>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/_authenticated/admin/settings')({
  component: SettingsPage,
});
