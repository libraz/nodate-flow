/**
 * /admin/settings -- Instance settings management.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { createFileRoute } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { apiRequest } from '../../../lib/api-client';

interface InstanceSetting {
  key: string;
  value: string;
}

interface SettingsResponse {
  items: InstanceSetting[];
}

const fieldStyle: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--nf-space-1, 0.25rem)',
};

const labelStyle: React.CSSProperties = {
  fontSize: 'var(--nf-text-sm, 0.875rem)',
  fontWeight: 600,
};

const descStyle: React.CSSProperties = {
  fontSize: 'var(--nf-text-xs, 0.75rem)',
  color: 'var(--nf-color-fg-muted, var(--color-muted))',
};

const inputStyle: React.CSSProperties = {
  padding: '0.5rem 0.75rem',
  borderRadius: 'var(--nf-radius-md, 0.375rem)',
  border: '1px solid var(--nf-color-border, var(--color-hairline))',
  background: 'var(--nf-color-bg, var(--color-bg))',
  color: 'var(--nf-color-fg, var(--color-fg))',
  fontSize: 'var(--nf-text-sm, 0.875rem)',
};

function SettingsPage(): ReactElement {
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
    void apiRequest<SettingsResponse>('/admin/settings').then((result) => {
      if (result.data) {
        for (const s of result.data.items) {
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
      registrationOpen: registrationOpen,
      mfaEnforcement: mfaEnforcement,
    };
    if (maxWorkspacesPerUser) {
      settings.maxWorkspacesPerUser = maxWorkspacesPerUser;
    }
    if (maxMembersPerWorkspace) {
      settings.maxMembersPerWorkspace = maxMembersPerWorkspace;
    }

    const result = await apiRequest<{ ok: boolean }>('/admin/settings', {
      method: 'PATCH',
      body: { settings },
    });
    setSaving(false);

    if (result.error) {
      setError(t('errors.generic'));
    } else {
      setSuccess(true);
    }
  };

  if (loading) {
    return (
      <p style={{ color: 'var(--nf-color-fg-muted, var(--color-muted))' }}>{t('common.loading')}</p>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-6, 1.5rem)' }}>
      <h1
        style={{
          fontFamily: 'var(--nf-font-display, var(--font-display))',
          fontSize: 'var(--nf-text-2xl, 1.5rem)',
          margin: 0,
        }}
      >
        {t('settings.title')}
      </h1>

      <div style={fieldStyle}>
        <label style={labelStyle}>
          {t('settings.registrationOpen')}
          <p style={descStyle}>{t('settings.registrationOpenDesc')}</p>
          <select
            value={registrationOpen}
            onChange={(e) => setRegistrationOpen(e.target.value)}
            style={inputStyle}
          >
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        </label>
      </div>

      <div style={fieldStyle}>
        <label style={labelStyle}>
          {t('settings.mfaEnforcement')}
          <p style={descStyle}>{t('settings.mfaEnforcementDesc')}</p>
          <select
            value={mfaEnforcement}
            onChange={(e) => setMfaEnforcement(e.target.value)}
            style={inputStyle}
          >
            <option value="none">{t('settings.mfaNone')}</option>
            <option value="optional">{t('settings.mfaOptional')}</option>
            <option value="required">{t('settings.mfaRequired')}</option>
          </select>
        </label>
      </div>

      <div style={fieldStyle}>
        <label style={labelStyle}>
          {t('settings.maxWorkspacesPerUser')}
          <input
            type="number"
            min="0"
            value={maxWorkspacesPerUser}
            onChange={(e) => setMaxWorkspacesPerUser(e.target.value)}
            style={inputStyle}
            placeholder="0 = unlimited"
          />
        </label>
      </div>

      <div style={fieldStyle}>
        <label style={labelStyle}>
          {t('settings.maxMembersPerWorkspace')}
          <input
            type="number"
            min="0"
            value={maxMembersPerWorkspace}
            onChange={(e) => setMaxMembersPerWorkspace(e.target.value)}
            style={inputStyle}
            placeholder="0 = unlimited"
          />
        </label>
      </div>

      {error ? (
        <p
          role="alert"
          style={{
            margin: 0,
            color: 'var(--nf-color-fg-danger, var(--color-danger))',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {error}
        </p>
      ) : null}

      {success ? (
        <output
          style={{
            margin: 0,
            color: 'var(--nf-color-fg-success, var(--color-success, green))',
            fontSize: 'var(--nf-text-sm, 0.875rem)',
          }}
        >
          {t('settings.saved')}
        </output>
      ) : null}

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
