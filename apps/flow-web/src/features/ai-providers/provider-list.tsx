/**
 * ProviderList — masked list of AI providers for a workspace, with add and
 * delete actions. Plaintext API keys never appear here.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import {
  type AiProvider,
  type AiProvidersQueryError,
  useAiProvidersQuery,
  useDeleteAiProvider,
} from './api';
import MaskedKey from './masked-key';
import ProviderAddDialog from './provider-add-dialog';
import styles from './provider-list.module.css';
import ProviderRotateDialog from './provider-rotate-dialog';

const NO_PROVIDERS: readonly AiProvider[] = [];

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
  const { entries, onExited } = usePresenceList(providers ?? NO_PROVIDERS, (p) => p.id);
  // Providers present at first paint; one added afterwards rises in.
  const [initialIds, setInitialIds] = useState<ReadonlySet<string> | null>(null);
  if (initialIds === null && providers !== undefined) {
    setInitialIds(new Set(providers.map((p) => p.id)));
  }
  const deletingId = del.isPending ? del.variables : undefined;

  const openAdd = (): void => {
    setAddOpen(true);
  };

  const handleDelete = async (providerId: string): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('providers.action.confirm_delete'),
      confirmLabel: t('providers.action.delete'),
    });
    if (!confirmed) return;
    del.mutate(providerId, {
      onSuccess: () => {
        toaster.show({ tone: 'success', message: t('providers.toast.deleted') });
      },
      onError: (err) => {
        toaster.show({ tone: 'danger', message: formatApiError(err, t, 'providers.toast.error') });
      },
    });
  };

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('providers.title')}</h1>
          <p className={ui.lede}>{t('providers.write_only_notice')}</p>
        </div>
        <Button variant="primary" disabled={isError} onClick={openAdd}>
          {t('providers.add')}
        </Button>
      </header>

      {isError ? (
        <EmptyState
          role="alert"
          titleAs="p"
          title={t(errorMessageKey(error))}
          className={ui.empty}
          action={
            error.status !== 403 ? (
              <Button
                size="sm"
                onClick={() => {
                  void refetch();
                }}
              >
                {t('providers.error.retry')}
              </Button>
            ) : undefined
          }
        />
      ) : isLoading || !providers ? (
        <div className={ui.skeletonStack} aria-hidden="true">
          <Skeleton className={styles.skeletonRow} />
          <Skeleton className={styles.skeletonRow} />
        </div>
      ) : providers.length === 0 ? (
        <EmptyState
          title={t('providers.empty')}
          className={ui.empty}
          action={<Button onClick={openAdd}>{t('providers.add')}</Button>}
        />
      ) : (
        <ul className={styles.list}>
          {entries.map(({ key, item: p, present }) => (
            <PresenceItem
              key={key}
              as="li"
              present={present}
              onExited={() => {
                onExited(key);
              }}
              className={initialIds !== null && !initialIds.has(p.id) ? styles.arrived : undefined}
            >
              <div className={styles.row}>
                <div className={styles.identity}>
                  <strong className={styles.name}>{p.name}</strong>
                  <span className={styles.meta}>
                    {kindLabel(p.kind)}
                    {p.defaultModel ? ` · ${p.defaultModel}` : ''}
                  </span>
                  {/*
                    The endpoint a provider talks to is part of what the
                    provider is, and it was the one configured field the
                    list never showed. An admin looking at this page
                    could not tell an openai_compat row pointed at their
                    gateway from one pointed anywhere else — nor spot a
                    kind=openai row carrying a base URL, which the server
                    now refuses to build and which used to take the whole
                    workspace's AI down with it.
                  */}
                  {p.baseUrl ? <TruncatedText text={p.baseUrl} className={styles.baseUrl} /> : null}
                  {p.kind === 'openai' && p.baseUrl ? (
                    <span role="status" className={styles.invalid}>
                      {t('providers.invalid_base_url')}
                    </span>
                  ) : null}
                  <MaskedKey value={p.apiKeyMasked} />
                </div>
                <div className={styles.actions}>
                  <Button
                    size="sm"
                    onClick={() => {
                      setRotateId(p.id);
                    }}
                  >
                    {t('providers.action.rotate')}
                  </Button>
                  <Button
                    size="sm"
                    variant="danger"
                    disabled={del.isPending}
                    aria-busy={deletingId === p.id}
                    onClick={() => {
                      void handleDelete(p.id);
                    }}
                  >
                    {t('providers.action.delete')}
                  </Button>
                </div>
              </div>
            </PresenceItem>
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
    </div>
  );
}
