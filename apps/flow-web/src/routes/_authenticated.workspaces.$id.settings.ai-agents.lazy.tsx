/**
 * /workspaces/$id/settings/ai-agents — workspace AI agent management (lazy).
 *
 * The page lists every ai_agents row for the workspace and exposes
 * four inline controls per agent:
 *   - schedule_kind dropdown (PATCH /ai/agents/{id}/schedule)
 *   - paused kill switch   (POST  /ai/agents/{id}/pause)
 *   - manual trigger       (POST  /ai/agents/{id}/trigger)
 *
 * Creation goes through a dialog that reads the workspace model
 * inventory from GET /ai/models and POSTs /ai/agents.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  AGENT_SCHEDULE_KINDS,
  type AgentScheduleKind,
  useAgentsQuery,
  useCreateAgent,
  useModelsQuery,
  usePauseAgent,
  useTriggerAgent,
  useUpdateAgentEventTriggers,
  useUpdateAgentSchedule,
} from '../features/ai-providers/agents-api';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-agents');

function AgentsList({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useAgentsQuery(workspaceId);
  const pauseMut = usePauseAgent();
  const scheduleMut = useUpdateAgentSchedule();
  const triggerMut = useTriggerAgent();
  const eventMut = useUpdateAgentEventTriggers();

  if (data.agents.length === 0) {
    return (
      <div
        style={{
          padding: '3rem 1rem',
          textAlign: 'center',
          color: 'var(--nf-color-fg-muted, var(--nf-color-fg-muted))',
          border: '1px dashed var(--nf-color-border, var(--nf-color-border))',
          borderRadius: '0.75rem',
          background: 'var(--nf-color-bg-sunken, transparent)',
        }}
      >
        {t('agents.empty')}
      </div>
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
        const handleTrigger = async (): Promise<void> => {
          try {
            await triggerMut.mutateAsync({ workspaceId, agentId: agent.id });
            toaster.show({ tone: 'success', message: t('agents.trigger.queued') });
          } catch {
            toaster.show({ tone: 'danger', message: t('agents.errors.triggerFailed') });
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
                    fontSize: 'var(--nf-text-xs)',
                    color: 'var(--nf-color-fg-muted)',
                    fontFamily: 'var(--font-mono)',
                  }}
                >
                  {agent.modelName}
                </code>
              </header>
              {agent.description ? (
                <p
                  style={{
                    margin: 0,
                    color: 'var(--nf-color-fg-muted)',
                    fontSize: 'var(--nf-text-sm)',
                  }}
                >
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
                    fontSize: 'var(--nf-text-sm)',
                  }}
                >
                  <label
                    htmlFor={`agent-schedule-${agent.id}`}
                    style={{ color: 'var(--nf-color-fg-muted)' }}
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
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    void handleTrigger();
                  }}
                  disabled={triggerMut.isPending || agent.paused}
                >
                  {t('agents.trigger.label')}
                </Button>
              </div>
              {agent.scheduleKind === 'on_event' ? (
                <EventTriggersEditor
                  workspaceId={workspaceId}
                  agentId={agent.id}
                  initial={agent.eventTriggerTypes ?? []}
                  pending={eventMut.isPending}
                  onSave={async (kinds) => {
                    try {
                      await eventMut.mutateAsync({
                        workspaceId,
                        agentId: agent.id,
                        eventTriggerTypes: kinds,
                      });
                      toaster.show({
                        tone: 'success',
                        message: t('agents.eventTriggers.saved'),
                      });
                    } catch {
                      toaster.show({
                        tone: 'danger',
                        message: t('agents.eventTriggers.failed'),
                      });
                    }
                  }}
                />
              ) : null}
            </Card>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * EventTriggersEditor is a free-form comma-separated text editor for
 * an agent's event_trigger_types JSON column. The eventbus kinds are
 * not enumerated client-side on purpose: new kinds get added by the
 * backend without a frontend deploy.
 */
function EventTriggersEditor({
  agentId,
  initial,
  pending,
  onSave,
}: {
  workspaceId: string;
  agentId: string;
  initial: string[];
  pending: boolean;
  onSave: (kinds: string[]) => Promise<void>;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const [value, setValue] = useState(initial.join(', '));
  return (
    <div
      style={{
        display: 'flex',
        gap: '0.5rem',
        alignItems: 'center',
        fontSize: 'var(--nf-text-sm)',
      }}
    >
      <label htmlFor={`agent-events-${agentId}`} style={{ color: 'var(--nf-color-fg-muted)' }}>
        {t('agents.eventTriggers.label')}
      </label>
      <Input
        id={`agent-events-${agentId}`}
        value={value}
        placeholder="task.updated, signal.attached"
        onChange={(e) => {
          setValue(e.target.value);
        }}
        style={{ flex: 1, minInlineSize: '16rem' }}
      />
      <Button
        type="button"
        size="sm"
        variant="ghost"
        disabled={pending}
        onClick={() => {
          const kinds = value
            .split(',')
            .map((k) => k.trim())
            .filter((k) => k.length > 0);
          void onSave(kinds);
        }}
      >
        {t('agents.eventTriggers.save')}
      </Button>
    </div>
  );
}

/**
 * CreateAgentForm is the inline form rendered when the operator
 * clicks "+ Agent". It reads the workspace model inventory via
 * useModelsQuery so the Select only offers models that exist. A
 * zero-model workspace gets a helpful empty state that links to the
 * ai-providers settings page instead of an unusable form.
 */
function CreateAgentForm({
  workspaceId,
  onDone,
}: {
  workspaceId: string;
  onDone: () => void;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { data: models } = useModelsQuery(workspaceId);
  const createMut = useCreateAgent();
  const [modelId, setModelId] = useState<string>(models.models[0]?.id ?? '');
  const [name, setName] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [scheduleKind, setScheduleKind] = useState<AgentScheduleKind>('disabled');
  const [eventTriggers, setEventTriggers] = useState('');

  if (models.models.length === 0) {
    return (
      <Card style={{ padding: '1rem' }}>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
          {t('agents.create.noModels')}
        </p>
      </Card>
    );
  }

  const handleSubmit = async (): Promise<void> => {
    if (!modelId || name.trim().length === 0 || systemPrompt.trim().length === 0) {
      toaster.show({ tone: 'warning', message: t('agents.create.missingFields') });
      return;
    }
    try {
      const kinds = eventTriggers
        .split(',')
        .map((k) => k.trim())
        .filter((k) => k.length > 0);
      await createMut.mutateAsync({
        workspaceId,
        modelId,
        name: name.trim(),
        systemPrompt: systemPrompt.trim(),
        scheduleKind,
        ...(scheduleKind === 'on_event' && kinds.length > 0 ? { eventTriggerTypes: kinds } : {}),
      });
      toaster.show({ tone: 'success', message: t('agents.create.success') });
      onDone();
    } catch {
      toaster.show({ tone: 'danger', message: t('agents.create.failed') });
    }
  };

  return (
    <Card
      style={{
        padding: '1rem',
        display: 'flex',
        flexDirection: 'column',
        gap: '0.75rem',
      }}
    >
      <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>{t('agents.create.title')}</h2>
      <FormField label={t('agents.create.name')}>
        {(control) => (
          <Input
            {...control}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            maxLength={255}
          />
        )}
      </FormField>
      <FormField label={t('agents.create.model')}>
        {(control) => (
          <Select
            {...control}
            value={modelId}
            onChange={(e) => {
              setModelId(e.target.value);
            }}
          >
            {models.models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.displayName} ({m.providerKind})
              </option>
            ))}
          </Select>
        )}
      </FormField>
      <FormField label={t('agents.create.systemPrompt')}>
        {(control) => (
          <Textarea
            {...control}
            value={systemPrompt}
            onChange={(e) => {
              setSystemPrompt(e.target.value);
            }}
            rows={5}
          />
        )}
      </FormField>
      <FormField label={t('agents.scheduleKind.label')}>
        {(control) => (
          <Select
            {...control}
            value={scheduleKind}
            onChange={(e) => {
              setScheduleKind(e.target.value as AgentScheduleKind);
            }}
          >
            {AGENT_SCHEDULE_KINDS.map((kind) => (
              <option key={kind} value={kind}>
                {t(`agents.scheduleKind.options.${kind}`)}
              </option>
            ))}
          </Select>
        )}
      </FormField>
      {scheduleKind === 'on_event' ? (
        <FormField label={t('agents.eventTriggers.label')}>
          {(control) => (
            <Input
              {...control}
              value={eventTriggers}
              onChange={(e) => {
                setEventTriggers(e.target.value);
              }}
              placeholder="task.updated, signal.attached"
            />
          )}
        </FormField>
      ) : null}
      <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t('agents.create.cancel')}
        </Button>
        <Button
          type="button"
          onClick={() => {
            void handleSubmit();
          }}
          disabled={createMut.isPending}
        >
          {t('agents.create.submit')}
        </Button>
      </div>
    </Card>
  );
}

function AiAgentsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t } = useTranslation('ai-suggestions');
  const [creating, setCreating] = useState(false);
  return (
    <section
      style={{ display: 'flex', flexDirection: 'column', gap: '1.5rem', inlineSize: '100%' }}
    >
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '0.5rem',
        }}
      >
        <div>
          <h1 style={{ margin: 0, fontSize: 'var(--nf-text-xl)' }}>{t('agents.title')}</h1>
          <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>{t('agents.subtitle')}</p>
        </div>
        {creating ? null : (
          <Button
            type="button"
            onClick={() => {
              setCreating(true);
            }}
          >
            {t('agents.create.open')}
          </Button>
        )}
      </header>
      {creating ? (
        <Suspense fallback={<Skeleton style={{ blockSize: '12rem', inlineSize: '100%' }} />}>
          <CreateAgentForm
            workspaceId={id}
            onDone={() => {
              setCreating(false);
            }}
          />
        </Suspense>
      ) : null}
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
