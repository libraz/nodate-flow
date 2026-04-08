/**
 * /workspaces/$id/settings/ai-agents — workspace AI agent management (lazy).
 *
 * The page lists every ai_agents row for the workspace and exposes
 * two inline controls:
 *   - schedule_kind dropdown (PATCH /ai/agents/{id}/schedule)
 *   - paused kill switch   (POST  /ai/agents/{id}/pause)
 *
 * Creation is not on the UI yet — agents are currently provisioned
 * via MCP / CLI / direct SQL. Once a `createAgent` endpoint lands,
 * a dialog will slot in here alongside the list.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';

import {
  AGENT_SCHEDULE_KINDS,
  type AgentScheduleKind,
  useAgentsQuery,
  usePauseAgent,
  useUpdateAgentSchedule,
} from '../features/ai-providers/agents-api';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-agents');

function AgentsList({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useAgentsQuery(workspaceId);
  const pauseMut = usePauseAgent();
  const scheduleMut = useUpdateAgentSchedule();

  if (data.agents.length === 0) {
    return (
      <Card style={{ padding: '1rem' }}>
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('agents.empty')}</p>
      </Card>
    );
  }

  return (
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
      {data.agents.map((agent) => {
        const handleScheduleChange = async (next: AgentScheduleKind): Promise<void> => {
          try {
            await scheduleMut.mutateAsync({
              workspaceId,
              agentId: agent.id,
              scheduleKind: next,
            });
          } catch {
            toaster.show({ tone: 'danger', message: t('agents.errors.scheduleFailed') });
          }
        };
        const handleTogglePause = async (): Promise<void> => {
          try {
            await pauseMut.mutateAsync({
              workspaceId,
              agentId: agent.id,
              paused: !agent.paused,
            });
          } catch {
            toaster.show({ tone: 'danger', message: t('agents.errors.pauseFailed') });
          }
        };
        return (
          <li key={agent.id}>
            <Card
              style={{
                padding: '1rem',
                display: 'flex',
                flexDirection: 'column',
                gap: '0.75rem',
              }}
            >
              <header
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  gap: '0.5rem',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                  <strong>{agent.name}</strong>
                  <Badge tone={agent.paused ? 'warning' : 'success'}>
                    {agent.paused ? t('agents.status.paused') : t('agents.status.active')}
                  </Badge>
                </div>
                <code
                  style={{
                    fontSize: '0.75rem',
                    color: 'var(--color-muted)',
                    fontFamily: 'var(--font-mono)',
                  }}
                >
                  {agent.modelName}
                </code>
              </header>
              {agent.description ? (
                <p style={{ margin: 0, color: 'var(--color-muted)', fontSize: '0.875rem' }}>
                  {agent.description}
                </p>
              ) : null}
              <div
                style={{
                  display: 'flex',
                  gap: '0.75rem',
                  alignItems: 'center',
                  flexWrap: 'wrap',
                }}
              >
                <div
                  style={{
                    display: 'flex',
                    gap: '0.5rem',
                    alignItems: 'center',
                    fontSize: '0.875rem',
                  }}
                >
                  <label
                    htmlFor={`agent-schedule-${agent.id}`}
                    style={{ color: 'var(--color-muted)' }}
                  >
                    {t('agents.scheduleKind.label')}
                  </label>
                  <Select
                    id={`agent-schedule-${agent.id}`}
                    value={agent.scheduleKind}
                    onChange={(e) => {
                      void handleScheduleChange(e.target.value as AgentScheduleKind);
                    }}
                    disabled={scheduleMut.isPending}
                  >
                    {AGENT_SCHEDULE_KINDS.map((kind) => (
                      <option key={kind} value={kind}>
                        {t(`agents.scheduleKind.options.${kind}`)}
                      </option>
                    ))}
                  </Select>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    void handleTogglePause();
                  }}
                  disabled={pauseMut.isPending}
                >
                  {agent.paused ? t('agents.resume') : t('agents.pause')}
                </Button>
              </div>
            </Card>
          </li>
        );
      })}
    </ul>
  );
}

function AiAgentsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t } = useTranslation('ai-suggestions');
  return (
    <section
      style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', inlineSize: '100%' }}
    >
      <header>
        <h1 style={{ margin: 0, fontSize: '1.25rem' }}>{t('agents.title')}</h1>
        <p style={{ margin: 0, color: 'var(--color-muted)' }}>{t('agents.subtitle')}</p>
      </header>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '6rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <AgentsList workspaceId={id} />
      </Suspense>
    </section>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-agents')({
  component: AiAgentsRoute,
});
