import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';

import { selectIsAuthenticated, useAuth } from '../features/auth/auth-store';
import { authSdk, flowWebUrl } from '../lib/sdk';
import { useWorkspace } from '../stores/workspace-store';

export const Route = createFileRoute('/setup')({
  component: SetupPage,
});

function slugify(text: string): string {
  return text
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

const centerPage: React.CSSProperties = {
  display: 'flex',
  minBlockSize: '100vh',
  alignItems: 'center',
  justifyContent: 'center',
  padding: '0 var(--nf-space-4)',
};

const cardStyle: React.CSSProperties = {
  width: '100%',
  maxWidth: '24rem',
  borderRadius: 'var(--nf-radius-lg)',
  padding: 'var(--nf-space-8)',
};

function SetupPage(): ReactElement {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isAuthenticated = useAuth(selectIsAuthenticated);
  const setWorkspace = useWorkspace((s) => s.setWorkspace);

  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugEdited, setSlugEdited] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    if (!isAuthenticated) {
      void navigate({ to: '/login' });
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const { data } = await authSdk.GET('/workspaces');
        if (cancelled) return;
        const body = data as
          | { workspaces?: Array<{ id: string; name: string; slug: string }>; total?: number }
          | undefined;
        if (body?.workspaces && body.workspaces.length > 0) {
          const ws = body.workspaces[0] as { id: string; name: string; slug: string };
          setWorkspace(ws.id, ws.name);
          window.location.replace(`${flowWebUrl}/calendar`);
          return;
        }
      } catch {
        // No workspaces endpoint or empty - show creation form
      }
      if (!cancelled) setChecking(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, navigate, setWorkspace]);

  function handleNameChange(value: string): void {
    setName(value);
    if (!slugEdited) {
      setSlug(slugify(value));
    }
  }

  async function handleSubmit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    if (!name.trim() || !slug.trim()) return;
    setError(null);
    setLoading(true);
    try {
      const { data, error: err } = await authSdk.POST('/workspaces', {
        body: { name: name.trim(), slug: slug.trim() },
      });
      if (err || !data) {
        const msg = (err as { detail?: string })?.detail ?? t('workspace.create_failed');
        setError(msg);
        return;
      }
      const ws = data as { id: string; name: string; slug: string };
      setWorkspace(ws.id, ws.name);
      window.location.replace(`${flowWebUrl}/calendar`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('workspace.create_failed'));
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div className="app-bg" style={centerPage}>
        <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
          {t('common.loading')}
        </p>
      </div>
    );
  }

  return (
    <div className="app-bg" style={centerPage}>
      <div className="glass-surface" style={cardStyle}>
        <h1
          style={{
            marginBlockEnd: 'var(--nf-space-2)',
            textAlign: 'center',
            fontSize: 'var(--nf-text-2xl)',
            fontWeight: 'var(--nf-weight-bold)',
            color: 'var(--nf-color-fg)',
          }}
        >
          {t('workspace.create')}
        </h1>
        <p
          style={{
            marginBlockEnd: 'var(--nf-space-8)',
            textAlign: 'center',
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-subtle)',
          }}
        >
          {t('workspace.description')}
        </p>
        <form
          onSubmit={handleSubmit}
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}
        >
          {error && (
            <div
              style={{
                borderRadius: 'var(--nf-radius-sm)',
                padding: 'var(--nf-space-3) var(--nf-space-4)',
                fontSize: 'var(--nf-text-sm)',
                backgroundColor: 'var(--nf-color-danger-subtle)',
                color: 'var(--nf-color-danger)',
              }}
            >
              {error}
            </div>
          )}
          <div>
            <label
              htmlFor="ws-name"
              style={{
                display: 'block',
                marginBlockEnd: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {t('workspace.name')}
            </label>
            <Input
              id="ws-name"
              type="text"
              required
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder={t('workspace.name_placeholder')}
              style={{ width: '100%' }}
            />
          </div>
          <div>
            <label
              htmlFor="ws-slug"
              style={{
                display: 'block',
                marginBlockEnd: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-sm)',
                fontWeight: 'var(--nf-weight-medium)',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {t('workspace.slug')}
            </label>
            <Input
              id="ws-slug"
              type="text"
              required
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
                setSlugEdited(true);
              }}
              placeholder={t('workspace.slug_placeholder')}
              style={{ width: '100%' }}
            />
            <p
              style={{
                marginBlockStart: 'var(--nf-space-1)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-subtle)',
              }}
            >
              {t('workspace.slug_hint')}
            </p>
          </div>
          <Button
            type="submit"
            variant="primary"
            disabled={loading || !name.trim() || !slug.trim()}
            style={{ width: '100%' }}
          >
            {loading ? t('workspace.creating') : t('workspace.create_button')}
          </Button>
        </form>
      </div>
    </div>
  );
}
