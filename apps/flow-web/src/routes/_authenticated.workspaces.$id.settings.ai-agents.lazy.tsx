/**
 * /workspaces/$id/settings/ai-agents — workspace AI agent management (lazy).
 *
 * The page lists every ai_agents row for the workspace and exposes
 * four inline controls per agent:
 *   - schedule_kind dropdown (PATCH /ai/agents/{id}/schedule)
 *   - paused kill switch   (POST  /ai/agents/{id}/pause)
 *   - manual trigger       (POST  /ai/agents/{id}/trigger)
 *
 * Creation goes through an inline form that reads the workspace model
 * inventory from GET /ai/models and POSTs /ai/agents.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Select from '@nodate-flow/ui/primitives/select';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { createLazyFileRoute, getRouteApi, Link } from '@tanstack/react-router';
import { type FormEvent, type ReactElement, Suspense, useState } from 'react';
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
import SaveStatus, { useSaveStatus } from '../features/settings/save-status';
import ui from '../features/settings/settings-ui.module.css';
import { formatApiError } from '../lib/api-error';
import styles from './_authenticated.workspaces.$id.settings.ai-agents.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/ai-agents');

function AgentsList({
  workspaceId,
  onCreate,
}: {
  workspaceId: string;
  onCreate: () => void;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useAgentsQuery(workspaceId);
  const pauseMut = usePauseAgent();
  const scheduleMut = useUpdateAgentSchedule();
  const triggerMut = useTriggerAgent();
  const eventMut = useUpdateAgentEventTriggers();
  const { entries, onExited } = usePresenceList(data.agents, (agent) => agent.id);
  // Agents present at first paint; one created afterwards rises in.
  const [initialIds] = useState<ReadonlySet<string>>(
    () => new Set(data.agents.map((agent) => agent.id)),
  );

  // Each control reports pending only for the agent it was used on.
  const pausingId = pauseMut.isPending ? pauseMut.variables?.agentId : undefined;
  const schedulingId = scheduleMut.isPending ? scheduleMut.variables?.agentId : undefined;
  const triggeringId = triggerMut.isPending ? triggerMut.variables?.agentId : undefined;
  const savingEventsId = eventMut.isPending ? eventMut.variables?.agentId : undefined;

  if (data.agents.length === 0) {
    return (
      <EmptyState
        titleAs="p"
        title={t('agents.empty')}
        className={ui.empty}
        action={
          <Button type="button" onClick={onCreate}>
            {t('agents.create.open')}
          </Button>
        }
      />
    );
  }

  return (
    <ul className={styles.list}>
      {entries.map(({ key, item: agent, present }) => {
        const handleScheduleChange = async (next: AgentScheduleKind): Promise<void> => {
          try {
            await scheduleMut.mutateAsync({
              workspaceId,
              agentId: agent.id,
              scheduleKind: next,
            });
          } catch (err) {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'agents.errors.scheduleFailed'),
            });
          }
        };
        const handleTogglePause = async (): Promise<void> => {
          try {
            await pauseMut.mutateAsync({
              workspaceId,
              agentId: agent.id,
              paused: !agent.paused,
            });
          } catch (err) {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'agents.errors.pauseFailed'),
            });
          }
        };
        const handleTrigger = async (): Promise<void> => {
          try {
            await triggerMut.mutateAsync({ workspaceId, agentId: agent.id });
            toaster.show({ tone: 'success', message: t('agents.trigger.queued') });
          } catch (err) {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'agents.errors.triggerFailed'),
            });
          }
        };
        const scheduleId = `agent-schedule-${agent.id}`;
        return (
          <PresenceItem
            key={key}
            as="li"
            present={present}
            onExited={() => {
              onExited(key);
            }}
            className={initialIds.has(agent.id) ? undefined : styles.arrived}
          >
            <article className={styles.card} aria-labelledby={`agent-name-${agent.id}`}>
              <header className={styles.cardHeader}>
                <div className={styles.cardTitle}>
                  <strong id={`agent-name-${agent.id}`}>{agent.name}</strong>
                  <Badge tone={agent.paused ? 'warning' : 'success'}>
                    {agent.paused ? t('agents.status.paused') : t('agents.status.active')}
                  </Badge>
                </div>
                <code className={styles.model}>{agent.modelName}</code>
              </header>
              {agent.description ? <p className={styles.description}>{agent.description}</p> : null}
              <div className={styles.controls}>
                <div className={styles.inlineField}>
                  <label htmlFor={scheduleId} className={styles.inlineLabel}>
                    {t('agents.scheduleKind.label')}
                  </label>
                  <Select
                    id={scheduleId}
                    value={agent.scheduleKind}
                    onChange={(e) => {
                      void handleScheduleChange(e.target.value as AgentScheduleKind);
                    }}
                    disabled={schedulingId === agent.id}
                    aria-busy={schedulingId === agent.id}
                  >
                    {AGENT_SCHEDULE_KINDS.map((kind) => (
                      <option key={kind} value={kind}>
                        {t(`agents.scheduleKind.options.${kind}`)}
                      </option>
                    ))}
                  </Select>
                </div>
                <div className={styles.controlActions}>
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => {
                      void handleTogglePause();
                    }}
                    disabled={pausingId === agent.id}
                    aria-busy={pausingId === agent.id}
                    aria-pressed={agent.paused}
                  >
                    {agent.paused ? t('agents.resume') : t('agents.pause')}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="primary"
                    onClick={() => {
                      void handleTrigger();
                    }}
                    disabled={triggeringId === agent.id || agent.paused}
                    aria-busy={triggeringId === agent.id}
                  >
                    {t('agents.trigger.label')}
                  </Button>
                </div>
              </div>
              {agent.scheduleKind === 'on_event' ? (
                <EventTriggersEditor
                  workspaceId={workspaceId}
                  agentId={agent.id}
                  initial={agent.eventTriggerTypes ?? []}
                  pending={savingEventsId === agent.id}
                  onSave={async (kinds) => {
                    try {
                      await eventMut.mutateAsync({
                        workspaceId,
                        agentId: agent.id,
                        eventTriggerTypes: kinds,
                      });
                      return true;
                    } catch (err) {
                      toaster.show({
                        tone: 'danger',
                        message: formatApiError(err, t, 'agents.eventTriggers.failed'),
                      });
                      return false;
                    }
                  }}
                />
              ) : null}
            </article>
          </PresenceItem>
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
  /** Resolves true once the triggers are stored. */
  onSave: (kinds: string[]) => Promise<boolean>;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const [value, setValue] = useState(initial.join(', '));
  const [baseline, setBaseline] = useState(value);
  const dirty = value !== baseline;
  const saveStatus = useSaveStatus(dirty);
  const inputId = `agent-events-${agentId}`;

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    const kinds = value
      .split(',')
      .map((k) => k.trim())
      .filter((k) => k.length > 0);
    if (await onSave(kinds)) {
      setBaseline(value);
      saveStatus.markSaved();
    }
  };

  return (
    <form
      className={styles.eventsForm}
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
    >
      <label htmlFor={inputId} className={styles.inlineLabel}>
        {t('agents.eventTriggers.label')}
      </label>
      <Input
        id={inputId}
        value={value}
        placeholder={t('agents.eventTriggers.placeholder')}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => {
          setValue(e.target.value);
        }}
        className={styles.eventsInput}
      />
      <SaveStatus saved={saveStatus.saved} dirty={dirty} />
      <Button type="submit" size="sm" disabled={pending} aria-busy={pending}>
        {t('agents.eventTriggers.save')}
      </Button>
    </form>
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
  const [missing, setMissing] = useState(false);

  if (models.models.length === 0) {
    return (
      <EmptyState
        titleAs="p"
        title={t('agents.create.noModels')}
        className={ui.empty}
        action={
          <Link to="/workspaces/$id/settings/ai-providers" params={{ id: workspaceId }}>
            {t('settings:nav.ai_providers')}
          </Link>
        }
      />
    );
  }

  const nameMissing = missing && name.trim().length === 0;
  const promptMissing = missing && systemPrompt.trim().length === 0;

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!modelId || name.trim().length === 0 || systemPrompt.trim().length === 0) {
      setMissing(true);
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
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'agents.create.failed') });
    }
  };

  return (
    <form
      className={styles.createForm}
      aria-labelledby="agent-create-title"
      noValidate
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
    >
      <h2 id="agent-create-title" className={ui.sectionTitle}>
        {t('agents.create.title')}
      </h2>
      <FormField
        label={t('agents.create.name')}
        required
        {...(nameMissing ? { error: t('agents.create.missingFields') } : {})}
      >
        {(control) => (
          <Input
            {...control}
            value={name}
            autoComplete="off"
            onChange={(e) => {
              setName(e.target.value);
            }}
            maxLength={255}
          />
        )}
      </FormField>
      <FormField label={t('agents.create.model')} required>
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
      <FormField
        label={t('agents.create.systemPrompt')}
        required
        {...(promptMissing ? { error: t('agents.create.missingFields') } : {})}
      >
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
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => {
                setEventTriggers(e.target.value);
              }}
              placeholder={t('agents.eventTriggers.placeholder')}
            />
          )}
        </FormField>
      ) : null}
      <div className={ui.actions}>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t('agents.create.cancel')}
        </Button>
        <Button
          type="submit"
          variant="primary"
          disabled={createMut.isPending}
          aria-busy={createMut.isPending}
        >
          {t('agents.create.submit')}
        </Button>
      </div>
    </form>
  );
}

function AiAgentsRoute(): ReactElement {
  const { id } = routeApi.useParams();
  const { t } = useTranslation('ai-suggestions');
  const [creating, setCreating] = useState(false);
  const openCreate = (): void => {
    setCreating(true);
  };
  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('agents.title')}</h1>
          <p className={ui.lede}>{t('agents.subtitle')}</p>
        </div>
        {creating ? null : (
          <Button type="button" variant="primary" onClick={openCreate}>
            {t('agents.create.open')}
          </Button>
        )}
      </header>
      {creating ? (
        <Suspense fallback={<Skeleton className={styles.skeletonForm} />}>
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
          <div className={ui.skeletonStack} aria-hidden="true">
            <Skeleton className={styles.skeletonCard} />
            <Skeleton className={styles.skeletonCard} />
          </div>
        }
      >
        <AgentsList workspaceId={id} onCreate={openCreate} />
      </Suspense>
    </div>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/settings/ai-agents')({
  component: AiAgentsRoute,
});
