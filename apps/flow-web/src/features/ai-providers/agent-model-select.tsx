/**
 * AgentModelSelect — inline model picker on an agent row. Lists the
 * workspace's enabled models; a model that is no longer offered (its model
 * or provider was disabled) stays visible as a disabled first option so the
 * row never shows a value the list cannot explain.
 */

import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import styles from './agent-model-select.module.css';
import { useModelsQuery } from './agents-api';
import { useUpdateAgentModel } from './models-api';

export interface AgentModelSelectProps {
  workspaceId: string;
  agentId: string;
  modelId: string;
  modelName: string;
}

export default function AgentModelSelect({
  workspaceId,
  agentId,
  modelId,
  modelName,
}: AgentModelSelectProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useModelsQuery(workspaceId);
  const update = useUpdateAgentModel(workspaceId);
  const available = data.models.some((m) => m.id === modelId);

  const handleChange = (next: string): void => {
    if (next === modelId) return;
    update.mutate(
      { agentId, modelId: next },
      {
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'agents.model.update_failed'),
          });
        },
      },
    );
  };

  return (
    <Select
      className={styles.select}
      aria-label={t('agents.model.label')}
      value={modelId}
      disabled={update.isPending}
      aria-busy={update.isPending}
      onChange={(e) => {
        handleChange(e.target.value);
      }}
    >
      {available ? null : (
        <option value={modelId} disabled>
          {t('agents.model.unavailable', { modelName })}
        </option>
      )}
      {data.models.map((m) => (
        <option key={m.id} value={m.id}>
          {t('agents.model.option', { displayName: m.displayName, providerName: m.providerName })}
        </option>
      ))}
    </Select>
  );
}
