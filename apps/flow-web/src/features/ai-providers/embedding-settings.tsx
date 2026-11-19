/** Workspace-level policy for using the server's task embedding provider. */

import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Switch from '@nodate-flow/ui/primitives/switch';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import styles from './embedding-settings.module.css';
import {
  type EmbeddingMode,
  type EmbeddingSettings,
  type EmbeddingSettingsQueryError,
  useEmbeddingSettingsQuery,
  useUpdateEmbeddingSettings,
} from './embedding-settings-api';

export interface EmbeddingSettingsPanelProps {
  workspaceId: string;
}

function statusKey(status: EmbeddingSettings['status']): string {
  switch (status) {
    case 'enabled':
      return 'providers.embedding.status.enabled';
    case 'disabled':
      return 'providers.embedding.status.disabled';
    case 'unconfigured':
      return 'providers.embedding.status.unconfigured';
  }
}

function queryErrorKey(error: EmbeddingSettingsQueryError): string {
  return error.status === 403
    ? 'providers.embedding.error.forbidden'
    : 'providers.embedding.error.load_failed';
}

export default function EmbeddingSettingsPanel({
  workspaceId,
}: EmbeddingSettingsPanelProps): ReactElement {
  const { t } = useTranslation('ai');
  const { data, isLoading, isError, error, refetch } = useEmbeddingSettingsQuery(workspaceId);
  const update = useUpdateEmbeddingSettings();
  const [modeOverride, setModeOverride] = useState<EmbeddingMode | undefined>(undefined);
  const mode = modeOverride ?? data?.mode ?? null;

  const dirty = data !== undefined && mode !== null && mode !== data.mode;
  const statusText = data ? t(statusKey(data.status)) : '';
  const detailError = update.error
    ? formatApiError(update.error, t, 'providers.embedding.error.update_failed')
    : null;

  const save = async (): Promise<void> => {
    if (mode === null || !dirty) return;
    await update.mutateAsync({ workspaceId, patch: { mode } });
    setModeOverride(undefined);
  };

  return (
    <section className={ui.section} aria-labelledby="embedding-settings-title">
      <div className={ui.sectionHeader}>
        <h2 id="embedding-settings-title" className={ui.sectionTitle}>
          {t('providers.embedding.title')}
        </h2>
        <p className={ui.sectionDescription}>{t('providers.embedding.description')}</p>
      </div>

      {isError ? (
        <EmptyState
          role="alert"
          titleAs="p"
          title={t(queryErrorKey(error))}
          className={ui.empty}
          action={
            error.status !== 403 ? (
              <Button
                size="sm"
                onClick={() => {
                  void refetch();
                }}
              >
                {t('providers.embedding.error.retry')}
              </Button>
            ) : undefined
          }
        />
      ) : isLoading || !data || mode === null ? (
        <div className={ui.skeletonStack} aria-hidden="true">
          <Skeleton className={styles.skeletonRow} />
        </div>
      ) : (
        <>
          <div className={styles.settingRow}>
            <div className={styles.settingCopy}>
              <strong className={styles.settingLabel}>{t('providers.embedding.mode_label')}</strong>
              <span className={styles.settingDescription}>
                {mode === 'inherit'
                  ? t('providers.embedding.mode.inherit')
                  : t('providers.embedding.mode.disabled')}
              </span>
            </div>
            <Switch
              checked={mode === 'inherit'}
              disabled={update.isPending}
              aria-label={t('providers.embedding.mode_label')}
              onCheckedChange={(enabled) => setModeOverride(enabled ? 'inherit' : 'disabled')}
            />
          </div>

          <dl className={styles.details}>
            <div className={styles.detailRow}>
              <dt>{t('providers.embedding.effective_status')}</dt>
              <dd>{statusText}</dd>
            </div>
            {data.status === 'enabled' && data.model ? (
              <div className={styles.detailRow}>
                <dt>{t('providers.embedding.model')}</dt>
                <dd>{data.model}</dd>
              </div>
            ) : null}
            {data.status === 'enabled' && data.endpoint ? (
              <div className={styles.detailRow}>
                <dt>{t('providers.embedding.endpoint')}</dt>
                <dd>
                  <TruncatedText text={data.endpoint} className={styles.endpoint} />
                </dd>
              </div>
            ) : null}
          </dl>

          <p className={styles.privacyNote}>{t('providers.embedding.data_use')}</p>

          {detailError ? (
            <p role="alert" className={styles.error}>
              {detailError}
            </p>
          ) : null}

          <div className={ui.actions}>
            <Button
              variant="primary"
              disabled={!dirty || update.isPending}
              aria-busy={update.isPending || undefined}
              onClick={() => {
                void save().catch(() => undefined);
              }}
            >
              {t('providers.embedding.save')}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
