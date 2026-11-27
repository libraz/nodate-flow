/**
 * ProviderModels — disclosure under a provider row listing the models
 * registered for it. Collapsed by default; the list is fetched only while
 * the section is open, then kept in the cache.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ChevronRight } from 'lucide-react';
import { type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import ModelAddDialog from './model-add-dialog';
import {
  type ProviderModel,
  usePatchProviderModel,
  useProviderModelsQuery,
  useSetProviderDefaultModel,
} from './models-api';
import styles from './provider-models.module.css';

export interface ProviderModelsProps {
  workspaceId: string;
  providerId: string;
  /** True for the provider AI features resolve to (the newest enabled one). */
  isWorkspaceDefault: boolean;
}

export default function ProviderModels({
  workspaceId,
  providerId,
  isWorkspaceDefault,
}: ProviderModelsProps): ReactElement {
  const { t } = useTranslation('ai');
  const [open, setOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const bodyId = useId();
  const query = useProviderModelsQuery(workspaceId, providerId, open);
  const patch = usePatchProviderModel(workspaceId, providerId);
  const setDefault = useSetProviderDefaultModel(workspaceId, providerId);
  const models = query.data;
  const hasDefault = models?.some((m) => m.isDefault && m.enabled) ?? false;
  const busyId = patch.isPending
    ? patch.variables?.modelId
    : setDefault.isPending
      ? setDefault.variables
      : undefined;

  const showError = (err: unknown): void => {
    toaster.show({ tone: 'danger', message: formatApiError(err, t, 'providers.toast.error') });
  };

  const handleToggle = (model: ProviderModel, next: boolean): void => {
    patch.mutate({ modelId: model.id, patch: { enabled: next } }, { onError: showError });
  };

  const handleSetDefault = (model: ProviderModel): void => {
    setDefault.mutate(model.id, { onError: showError });
  };

  return (
    <section className={styles.root}>
      <div className={styles.bar}>
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={open}
          aria-controls={bodyId}
          onClick={() => {
            setOpen((prev) => !prev);
          }}
        >
          <ChevronRight size={14} aria-hidden className={styles.chevron} />
          <span>{t('providers.models.title')}</span>
          {models ? <span className={styles.count}>{models.length}</span> : null}
        </button>
        {models && !hasDefault ? (
          <span className={styles.meta}>{t('providers.models.no_default')}</span>
        ) : null}
      </div>
      <Collapse open={open} id={bodyId}>
        <div className={styles.body}>
          {isWorkspaceDefault ? (
            <p className={styles.note}>{t('providers.models.workspace_default_note')}</p>
          ) : null}
          {query.isError ? (
            <p role="alert" className={styles.note}>
              {formatApiError(query.error, t, 'providers.toast.error')}
            </p>
          ) : models === undefined ? (
            <div aria-hidden="true">
              <Skeleton className={styles.skeletonRow} />
            </div>
          ) : models.length === 0 ? (
            <p className={styles.note}>{t('providers.models.empty')}</p>
          ) : (
            <ul className={styles.list}>
              {models.map((m) => (
                <li key={m.id} className={styles.item} data-disabled={m.enabled ? undefined : ''}>
                  <div className={styles.identity}>
                    <span className={styles.displayName}>{m.displayName}</span>
                    <code className={styles.modelName}>{m.name}</code>
                  </div>
                  <div className={styles.state}>
                    {m.isDefault ? (
                      <Badge tone="accent">{t('providers.models.default_badge')}</Badge>
                    ) : null}
                    {m.agentCount > 0 ? (
                      <span className={styles.meta}>
                        {t('providers.models.in_use', { count: m.agentCount })}
                      </span>
                    ) : null}
                    {m.enabled ? null : (
                      <span className={styles.meta}>{t('providers.models.disabled')}</span>
                    )}
                    {m.enabled && !m.isDefault ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busyId === m.id}
                        onClick={() => {
                          handleSetDefault(m);
                        }}
                      >
                        {t('providers.models.set_default')}
                      </Button>
                    ) : null}
                    <Switch
                      checked={m.enabled}
                      disabled={busyId === m.id}
                      aria-label={t('providers.models.enable_toggle', { name: m.displayName })}
                      onCheckedChange={(next) => {
                        handleToggle(m, next);
                      }}
                    />
                  </div>
                </li>
              ))}
            </ul>
          )}
          <div className={styles.footer}>
            <Button
              size="sm"
              onClick={() => {
                setAddOpen(true);
              }}
            >
              {t('providers.models.add')}
            </Button>
          </div>
        </div>
      </Collapse>
      <ModelAddDialog
        workspaceId={workspaceId}
        providerId={providerId}
        open={addOpen}
        registeredNames={models?.map((m) => m.name) ?? []}
        onClose={() => {
          setAddOpen(false);
        }}
      />
    </section>
  );
}
