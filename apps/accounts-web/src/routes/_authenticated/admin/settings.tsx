/**
 * /admin/settings -- Instance settings management.
 */

import type { components } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { apiRequest } from '../../../lib/api';
import { ApiError, NetworkError } from '../../../lib/api-error';
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

/**
 * Turns a refused save into the line shown above the form. The
 * requester has already read the envelope, so the message it carries
 * is the server's own wording; the code is appended only when there is
 * nothing better to say than the generic line.
 */
function formatSettingsSaveError(err: unknown, fallback: string): string {
  // A request that never arrived is an ApiError as well, and the message
  // it carries is the English line handed to the requester rather than a
  // server's wording, so there is nothing here worth showing over the
  // translated one.
  if (err instanceof NetworkError) return fallback;
  if (!(err instanceof ApiError)) return fallback;
  if (err.message.length > 0 && err.message !== fallback) return err.message;
  return err.code ? `${fallback} (${err.code})` : fallback;
}

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
    void apiRequest((client) => client.GET('/admin/settings'), 'Failed to load settings', {
      onError: 'empty',
      empty: null,
    }).then((result) => {
      if (result) {
        const body = result as SettingsResponse;
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

    try {
      await apiRequest(
        (client) => client.PATCH('/admin/settings', { body: { settings } }),
        'Failed to save settings',
      );
      setSuccess(true);
    } catch (err) {
      setError(formatSettingsSaveError(err, t('errors.generic')));
    } finally {
      setSaving(false);
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
