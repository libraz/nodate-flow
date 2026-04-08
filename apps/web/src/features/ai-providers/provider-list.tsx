/**
 * ProviderList — masked list of AI providers for a workspace, with add and
 * delete actions. Plaintext API keys never appear here.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useAiProvidersQuery, useDeleteAiProvider } from './api';
import MaskedKey from './masked-key';
import ProviderAddDialog from './provider-add-dialog';
import ProviderRotateDialog from './provider-rotate-dialog';

function useKindLabel(): (kind: string) => string {
  const { t } = useTranslation('ai');
  return (kind: string): string => {
    switch (kind) {
      case 'anthropic':
        return t('providers.kind.anthropic');
      case 'openai':
        return t('providers.kind.openai');
      case 'google':
        return t('providers.kind.google');
      case 'ollama':
        return t('providers.kind.ollama');
      case 'openai_compat':
        return t('providers.kind.openai_compat');
      default:
        return kind;
    }
  };
}

export interface ProviderListProps {
  workspaceId: string;
}

export default function ProviderList({ workspaceId }: ProviderListProps): ReactElement {
  const { t } = useTranslation('ai');
  const { data: providers } = useAiProvidersQuery(workspaceId);
  const del = useDeleteAiProvider(workspaceId);
  const [addOpen, setAddOpen] = useState(false);
  const [rotateId, setRotateId] = useState<string | null>(null);
  const kindLabel = useKindLabel();

  const handleDelete = (providerId: string): void => {
    if (!window.confirm(t('providers.action.confirm_delete'))) return;
    del.mutate(providerId, {
      onSuccess: () => {
        toaster.show({ tone: 'success', message: t('providers.toast.deleted') });
      },
      onError: () => {
        toaster.show({ tone: 'danger', message: t('providers.toast.error') });
      },
    });
  };

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '1rem',
        }}
      >
        <h1 style={{ margin: 0, fontFamily: 'var(--font-display)', fontSize: '1.5rem' }}>
          {t('providers.title')}
        </h1>
        <Button
          variant="primary"
          onClick={() => {
            setAddOpen(true);
          }}
        >
          {t('providers.add')}
        </Button>
      </header>

      <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.875rem' }}>
        {t('providers.write_only_notice')}
      </p>

      {providers.length === 0 ? (
        <Card style={{ padding: '2rem', textAlign: 'center', color: 'var(--color-muted)' }}>
          {t('providers.empty')}
        </Card>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.75rem',
          }}
        >
          {providers.map((p) => (
            <li key={p.id}>
              <Card style={{ padding: '1rem' }}>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    gap: '1rem',
                    flexWrap: 'wrap',
                  }}
                >
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                    <strong>{p.name}</strong>
                    <span style={{ color: 'var(--color-muted)', fontSize: '0.875rem' }}>
                      {kindLabel(p.kind)}
                      {p.defaultModel ? ` · ${p.defaultModel}` : ''}
                    </span>
                    <MaskedKey value={p.apiKeyMasked} />
                  </div>
                  <div style={{ display: 'flex', gap: '0.5rem' }}>
                    <Button
                      variant="ghost"
                      onClick={() => {
                        setRotateId(p.id);
                      }}
                    >
                      {t('providers.action.rotate')}
                    </Button>
                    <Button
                      variant="danger"
                      onClick={() => {
                        handleDelete(p.id);
                      }}
                    >
                      {t('providers.action.delete')}
                    </Button>
                  </div>
                </div>
              </Card>
            </li>
          ))}
        </ul>
      )}

      <ProviderAddDialog
        workspaceId={workspaceId}
        open={addOpen}
        onClose={() => {
          setAddOpen(false);
        }}
      />
      <ProviderRotateDialog
        workspaceId={workspaceId}
        providerId={rotateId}
        open={rotateId !== null}
        onClose={() => {
          setRotateId(null);
        }}
      />
    </section>
  );
}
