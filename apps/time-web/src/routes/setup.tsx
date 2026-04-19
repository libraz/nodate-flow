import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { type ReactElement, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { workspaceApi } from '../lib/api-client';
import { useAuthStore } from '../stores/auth-store';
import { useWorkspaceStore } from '../stores/workspace-store';

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

function SetupPage(): ReactElement {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const setWorkspace = useWorkspaceStore((s) => s.setWorkspace);

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
        const res = await workspaceApi.list();
        if (cancelled) return;
        if (res.items && res.items.length > 0) {
          const ws = res.items[0] as { id: string; name: string; slug: string };
          setWorkspace(ws.id, ws.name);
          void navigate({ to: '/calendar' });
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
      const ws = await workspaceApi.create({ name: name.trim(), slug: slug.trim() });
      setWorkspace(ws.id, ws.name);
      void navigate({ to: '/calendar' });
    } catch (err) {
      setError(err instanceof Error ? err.message : t('workspace.createFailed'));
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div className="app-bg flex min-h-screen items-center justify-center">
        <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
          {t('common.loading')}
        </p>
      </div>
    );
  }

  return (
    <div className="app-bg flex min-h-screen items-center justify-center px-4">
      <div className="glass-surface w-full max-w-sm rounded-[var(--radius-xl)] p-8">
        <h1
          className="mb-2 text-center text-2xl font-bold"
          style={{ color: 'var(--color-text-primary)' }}
        >
          {t('workspace.create')}
        </h1>
        <p className="mb-8 text-center text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
          {t('workspace.description')}
        </p>
        <form onSubmit={handleSubmit} className="space-y-4">
          {error && (
            <div
              className="rounded-[var(--radius-sm)] px-4 py-3 text-sm"
              style={{ backgroundColor: 'var(--color-danger-bg)', color: 'var(--color-danger)' }}
            >
              {error}
            </div>
          )}
          <div>
            <label
              htmlFor="ws-name"
              className="mb-1 block text-sm font-medium"
              style={{ color: 'var(--color-text-secondary)' }}
            >
              {t('workspace.name')}
            </label>
            <input
              id="ws-name"
              type="text"
              required
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              className="input-modern w-full"
              placeholder={t('workspace.namePlaceholder')}
            />
          </div>
          <div>
            <label
              htmlFor="ws-slug"
              className="mb-1 block text-sm font-medium"
              style={{ color: 'var(--color-text-secondary)' }}
            >
              {t('workspace.slug')}
            </label>
            <input
              id="ws-slug"
              type="text"
              required
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
                setSlugEdited(true);
              }}
              className="input-modern w-full"
              placeholder={t('workspace.slugPlaceholder')}
            />
            <p className="mt-1 text-xs" style={{ color: 'var(--color-text-tertiary)' }}>
              {t('workspace.slugHint')}
            </p>
          </div>
          <button
            type="submit"
            disabled={loading || !name.trim() || !slug.trim()}
            className="btn-primary w-full"
          >
            {loading ? t('workspace.creating') : t('workspace.createButton')}
          </button>
        </form>
      </div>
    </div>
  );
}
