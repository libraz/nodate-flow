/**
 * ProviderList — masked list of AI providers for a workspace, with add and
 * delete actions. Plaintext API keys never appear here.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { confirmAction } from '../../lib/confirm-action';
import { type AiProvidersQueryError, useAiProvidersQuery, useDeleteAiProvider } from './api';
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

/**
 * errorMessageKey picks a localized copy key based on the HTTP status
 * of the underlying query failure. 403 gets a gentler "admin only"
 * message; everything else (404, 5xx, network, ...) gets the generic
 * "failed to load" copy.
 */
function errorMessageKey(
  error: AiProvidersQueryError,
): 'providers.error.forbidden' | 'providers.error.load_failed' {
  if (error.status === 403) return 'providers.error.forbidden';
  return 'providers.error.load_failed';
}

export default function ProviderList({ workspaceId }: ProviderListProps): ReactElement {
  const { t } = useTranslation('ai');
  const { data: providers, isLoading, isError, error, refetch } = useAiProvidersQuery(workspaceId);
  const del = useDeleteAiProvider(workspaceId);
  const [addOpen, setAddOpen] = useState(false);
  const [rotateId, setRotateId] = useState<string | null>(null);
  const kindLabel = useKindLabel();

  const handleDelete = async (providerId: string): Promise<void> => {
    if (!(await confirmAction({ message: t('providers.action.confirm_delete') }))) return;
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
          disabled={isError}
          onClick={() => {
            setAddOpen(true);
          }}
        >
          {t('providers.add')}
        </Button>
      </header>

      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
        {t('providers.write_only_notice')}
      </p>

      {isError ? (
        <Card
          role="alert"
          style={{
            padding: '1.5rem',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: '0.75rem',
            textAlign: 'center',
          }}
        >
          <p style={{ margin: 0, color: 'var(--nf-color-fg)' }}>{t(errorMessageKey(error))}</p>
          {error.status !== 403 ? (
            <Button
              variant="ghost"
              onClick={() => {
                void refetch();
              }}
            >
              {t('providers.error.retry')}
            </Button>
          ) : null}
        </Card>
      ) : isLoading || !providers ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
          <Skeleton style={{ blockSize: '4rem', inlineSize: '100%' }} />
        </div>
      ) : providers.length === 0 ? (
        <Card style={{ padding: '2rem', textAlign: 'center', color: 'var(--nf-color-fg-muted)' }}>
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
                    <span style={{ color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
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
                        void handleDelete(p.id);
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
