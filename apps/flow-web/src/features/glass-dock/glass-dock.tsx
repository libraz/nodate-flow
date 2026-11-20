/**
 * GlassDock — fixed bottom-right pill that surfaces pending AI suggestions.
 *
 * Collapsed: a small button showing the Sparkles icon + a count badge.
 * Expanded: a ~360px panel listing recent suggestions across the active
 * workspace, sourced from `useAiSuggestionsQuery` which polls the backend
 * every 30s. Apply / dismiss persist via the AI suggestions endpoints so
 * the dock reflects cross-device activity.
 */

import { useFocusTrap } from '@nodate-flow/ui/hooks/use-focus-trap';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Day, type Zone } from '@nodate-flow/ui/time';
import { Link, useMatches, useNavigate } from '@tanstack/react-router';
import type { TFunction } from 'i18next';
import { Sparkles, X } from 'lucide-react';
import { type ReactElement, useEffect, useId, useRef, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';
import PageHelp from '../../components/page-help';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { useEffectiveZone } from '../../lib/use-effective-timezone';

import { useAiSuggestionsQuery } from '../ai-suggestions/api';
import EditSuggestionDialog, {
  type EditSuggestionPatch,
} from '../ai-suggestions/edit-suggestion-dialog';
import { recommendedActionLabel } from '../ai-suggestions/recommended-action-label';
import type { Suggestion } from '../ai-suggestions/store';
import SuggestionActions from '../ai-suggestions/suggestion-actions';
import { useSuggestionActionExecutor } from '../ai-suggestions/use-suggestion-action-executor';
import { selectUser, useAuth } from '../auth/auth-store';
import { type TaskAutoAction, useAutoActionsQuery } from '../auto-actions/api';
import { type CompileLensResult, NlQueryError, useCompileLens } from '../nl-query/api';
import { type StateSuggestion, useStateSuggestionsQuery } from '../nl-query/state-suggestions';
import { useWorkspaceStream } from '../realtime/use-workspace-stream';
import { type TaskReminder, useRemindersQuery } from '../reminders/api';
import type { TaskDerivedState, TaskPriority } from '../tasks/api';
import { setTaskFilters } from '../tasks/use-task-filters';
import styles from './glass-dock.module.css';

const NL_UNPARSEABLE = 'AI.NL_QUERY.UNPARSEABLE';

/** Map a 0-1 confidence score to a qualitative i18n key. */
function confidenceLabel(score: number, t: (key: string) => string): string {
  if (score >= 0.8) return t('confidence.high');
  if (score >= 0.5) return t('confidence.medium');
  return t('confidence.low');
}

/**
 * Format a `YYYY-MM-DD` date as a relative expression, counted from
 * today in `zone`.
 *
 * "Today" is a calendar day, so a reminder read in the browser's zone
 * says "overdue" or "today" up to a day away from what the calendar and
 * the server-side reminder about it say. Both operands are days here, so
 * the difference never goes through an instant and a DST transition
 * cannot make a day 23 or 25 hours long.
 */
function formatRelativeDate(dateStr: string, zone: Zone, t: TFunction): string {
  const target = Day.parse(dateStr);
  if (!target) return '';
  const diffDays = target.diffDays(Day.today(zone));
  if (diffDays < 0) return String(t('relative_date.overdue', { count: Math.abs(diffDays) }));
  if (diffDays === 0) return String(t('relative_date.today'));
  return String(t('relative_date.in_days', { count: diffDays }));
}

/** Produce a human-readable label for a single filter condition. */
function formatFilterCondition(
  field: string,
  operator: string,
  value: unknown,
  t: TFunction,
): string {
  if (field === 'priority' && operator === 'eq' && typeof value === 'number') {
    const label = String(t(`priority_label.${value}`));
    return String(t('filter_condition.priority', { label }));
  }
  if (field === 'blocked' && operator === 'eq' && value === true) {
    return String(t('filter_condition.blocked'));
  }
  if (field === 'due_on' && operator === 'between' && value === 'this_week') {
    return String(t('filter_condition.due_this_week'));
  }
  if (field === 'status' && operator === 'in' && Array.isArray(value)) {
    return String(t('filter_condition.status', { values: (value as string[]).join(', ') }));
  }
  const display = Array.isArray(value) ? (value as unknown[]).join(', ') : String(value ?? '');
  return `${field} ${operator} ${display}`;
}

/** The only lens fields `applyLensFilter` maps onto the project task filters. */
function isAppliedCondition(field: string, operator: string): boolean {
  return (field === 'priority' || field === 'status') && (operator === 'eq' || operator === 'in');
}

function useActiveProjectId(): string | undefined {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i -= 1) {
    const params = matches[i]?.params as Record<string, string> | undefined;
    if (!params) continue;
    const id = params.projectId;
    if (typeof id === 'string' && id.length > 0) return id;
  }
  return undefined;
}

function NlQueryPanel({ workspaceId }: { workspaceId: string | undefined }): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const navigate = useNavigate();
  const projectId = useActiveProjectId();
  const [prompt, setPrompt] = useState('');
  const [result, setResult] = useState<CompileLensResult | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const mutation = useCompileLens();
  const hintId = useId();

  const disabled = !workspaceId || prompt.trim().length === 0 || mutation.isPending;

  /** Extract TaskFilters from a Lens and apply them. */
  const applyLensFilter = (lens: { filter?: Record<string, Record<string, unknown>> }): void => {
    if (!projectId || !workspaceId) return;
    const priorityFilter = lens.filter?.priority;
    const statusFilter = lens.filter?.status;
    const priority: TaskPriority[] = [];
    if (priorityFilter) {
      const v = priorityFilter.eq ?? priorityFilter.in;
      if (typeof v === 'number') priority.push(v as TaskPriority);
      if (Array.isArray(v)) priority.push(...(v as TaskPriority[]));
    }
    const states: TaskDerivedState[] = [];
    if (statusFilter) {
      const v = statusFilter.eq ?? statusFilter.in;
      if (typeof v === 'string') states.push(v as TaskDerivedState);
      if (Array.isArray(v)) states.push(...(v as TaskDerivedState[]));
    }
    const filters: Parameters<typeof setTaskFilters>[1] = {
      search: '',
      assigneeId: '',
    };
    if (priority.length > 0) filters.priority = priority;
    if (states.length > 0) filters.states = states;
    setTaskFilters(projectId, filters);
    void navigate({
      to: '/workspaces/$id/projects/$projectId/tasks',
      params: { id: workspaceId, projectId },
    });
  };

  const handleSubmit = (event: React.FormEvent): void => {
    event.preventDefault();
    if (!workspaceId || prompt.trim().length === 0) return;
    setErrorMsg(null);
    setResult(null);
    mutation.mutate(
      { workspaceId, prompt: prompt.trim() },
      {
        onSuccess: (r) => {
          setResult(r);
          // Automatically apply the filter — no extra click needed.
          if (r.lens) {
            applyLensFilter(r.lens as { filter?: Record<string, Record<string, unknown>> });
          }
        },
        onError: (err) => {
          if (err instanceof NlQueryError && err.code?.includes(NL_UNPARSEABLE)) {
            setErrorMsg(t('nl_query.unparseable'));
          } else {
            setErrorMsg(t('nl_query.error'));
          }
        },
      },
    );
  };

  // Derive badge data from result.lens
  const filterBadges: Array<{ key: string; label: string; applied: boolean }> = [];
  // Filters land only when a project is open; see applyLensFilter.
  const canApply = Boolean(projectId && workspaceId);
  if (result?.lens) {
    const lens = result.lens as {
      filter?: Record<string, Record<string, unknown>>;
      sort?: Array<{ field: string; dir: string }>;
      groupBy?: string | null;
    };
    if (lens.filter) {
      for (const [field, conditions] of Object.entries(lens.filter)) {
        for (const [op, value] of Object.entries(conditions)) {
          filterBadges.push({
            key: `${field}-${op}`,
            label: formatFilterCondition(field, op, value, t),
            applied: canApply && isAppliedCondition(field, op),
          });
        }
      }
    }
    if (lens.sort && lens.sort.length > 0) {
      const sortText = lens.sort.map((s) => `${s.field} ${s.dir}`).join(', ');
      filterBadges.push({
        key: '_sort',
        label: `${t('nl_query.sort_label')}: ${sortText}`,
        applied: false,
      });
    }
    if (lens.groupBy) {
      filterBadges.push({
        key: '_group',
        label: `${t('nl_query.group_label')}: ${lens.groupBy}`,
        applied: false,
      });
    }
  }

  const appliedBadges = filterBadges.filter((badge) => badge.applied);
  const referenceBadges = filterBadges.filter((badge) => !badge.applied);

  return (
    <form
      onSubmit={handleSubmit}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
        padding: 'var(--nf-space-3)',
        borderBlockEnd: '1px solid var(--nf-color-border)',
      }}
    >
      <label
        htmlFor="nl-query-input"
        style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-muted)' }}
      >
        {t('nl_query.label')}
      </label>
      <p id={hintId} className={styles.fieldHint}>
        {t('nl_query.hint')}
      </p>
      <input
        data-1p-ignore
        id="nl-query-input"
        type="text"
        autoComplete="off"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        placeholder={t('nl_query.placeholder')}
        aria-describedby={hintId}
        maxLength={500}
        style={{
          padding: 'var(--nf-space-2) var(--nf-space-2-5)',
          borderRadius: 'var(--nf-radius-sm)',
          border: '1px solid var(--nf-color-border)',
          background: 'var(--nf-color-bg)',
          color: 'var(--nf-color-fg)',
          fontSize: 'var(--nf-text-supporting)',
        }}
      />
      <button
        type="submit"
        disabled={disabled}
        style={{
          padding: 'var(--nf-space-1-5) var(--nf-space-3)',
          borderRadius: 'var(--nf-radius-sm)',
          border: '1px solid var(--nf-color-border)',
          background: 'var(--nf-color-surface)',
          color: 'var(--nf-color-fg)',
          fontSize: 'var(--nf-text-xs)',
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.6 : 1,
        }}
      >
        {mutation.isPending ? t('nl_query.compiling') : t('nl_query.submit')}
      </button>
      {errorMsg ? (
        <p
          role="alert"
          style={{ margin: 0, fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-danger-fg)' }}
        >
          {errorMsg}
        </p>
      ) : null}
      {filterBadges.length > 0 ? (
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-2)',
          }}
        >
          {appliedBadges.length > 0 ? (
            <div className={styles.conditionGroup}>
              <span className={styles.conditionGroupLabel}>{t('nl_query.applied_group')}</span>
              <ul className={styles.conditionList}>
                {appliedBadges.map((badge) => (
                  <li key={badge.key} className={styles.conditionApplied}>
                    {badge.label}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {referenceBadges.length > 0 ? (
            <div className={styles.conditionGroup}>
              <span className={styles.conditionGroupLabel}>{t('nl_query.reference_group')}</span>
              <ul className={styles.conditionList}>
                {referenceBadges.map((badge) => (
                  <li key={badge.key} className={styles.conditionReference}>
                    {badge.label}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-micro)',
              color: 'var(--nf-color-fg-muted)',
            }}
          >
            {t('nl_query.applied')}
          </p>
        </div>
      ) : null}
    </form>
  );
}

function GlassDockSuggestionRow({
  suggestion,
  disabled,
  onApply,
  onEdit,
  onDismiss,
}: {
  suggestion: Suggestion;
  disabled: boolean;
  onApply: (suggestion: Suggestion) => void;
  onEdit: (suggestion: Suggestion) => void;
  onDismiss: (suggestion: Suggestion) => void;
}): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const handleApply = (): void => {
    onApply(suggestion);
  };
  const handleEdit = (): void => {
    onEdit(suggestion);
  };
  const handleDismiss = (): void => {
    onDismiss(suggestion);
  };
  const needsRefresh = suggestion.sourceVersionStatus !== 'captured' || !suggestion.proposalId;
  return (
    <Card className={styles.suggestionCard}>
      <div className={styles.suggestionBody}>
        <div className={styles.suggestionMeta}>
          <Badge
            tone={
              suggestion.score >= 0.8 ? 'danger' : suggestion.score >= 0.5 ? 'warning' : 'neutral'
            }
          >
            {confidenceLabel(suggestion.score, t)}
          </Badge>
          <span className={styles.suggestionAction}>
            {recommendedActionLabel(suggestion.recommendedAction, t)}
          </span>
        </div>
        <p className={styles.suggestionReasoning}>{suggestion.reasoning}</p>
        {needsRefresh ? (
          <p role="note" className={styles.suggestionAction} style={{ margin: 0 }}>
            {t('triage.legacy_notice')}
          </p>
        ) : null}
        <SuggestionActions
          disabled={disabled || needsRefresh}
          onApply={handleApply}
          onDismiss={handleDismiss}
          onEdit={handleEdit}
        />
      </div>
    </Card>
  );
}

function StateSuggestionsPanel({
  workspaceId,
}: {
  workspaceId: string | undefined;
}): ReactElement | null {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useStateSuggestionsQuery(workspaceId);
  const items: StateSuggestion[] = data ?? [];
  if (items.length === 0) return null;
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-1-5)',
        padding: 'var(--nf-space-3)',
        borderBlockEnd: '1px solid var(--nf-color-border)',
      }}
    >
      <strong style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-muted)' }}>
        {t('state_suggestions.title')}
      </strong>
      <ul
        style={{
          listStyle: 'none',
          padding: 0,
          margin: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1-5)',
        }}
      >
        {items.slice(0, 5).map((s) => (
          <li key={s.taskId}>
            <a
              href={`/tasks/${s.taskId}`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--nf-space-2)',
                color: 'var(--nf-color-fg)',
                textDecoration: 'none',
                fontSize: 'var(--nf-text-xs)',
              }}
            >
              <Badge tone="accent">
                {t(`transition_label.${s.transition}`, { defaultValue: s.transition })}
              </Badge>
              <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {s.title}
              </span>
              <Badge
                tone={s.confidence >= 0.8 ? 'danger' : s.confidence >= 0.5 ? 'warning' : 'neutral'}
              >
                {confidenceLabel(s.confidence, t)}
              </Badge>
            </a>
          </li>
        ))}
      </ul>
    </div>
  );
}

function RemindersPanel({ workspaceId }: { workspaceId: string | undefined }): ReactElement | null {
  const { t } = useTranslation('ai-suggestions');
  const zone = useEffectiveZone();
  const { data } = useRemindersQuery(workspaceId);
  const items: TaskReminder[] = data ?? [];
  if (items.length === 0) return null;
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-1-5)',
        padding: 'var(--nf-space-3)',
        borderBlockEnd: '1px solid var(--nf-color-border)',
      }}
    >
      <strong style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-muted)' }}>
        {t('reminders.title')}
      </strong>
      <ul
        style={{
          listStyle: 'none',
          padding: 0,
          margin: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1-5)',
        }}
      >
        {items.slice(0, 5).map((r) => {
          const tone =
            r.kind === 'overdue' ? 'danger' : r.kind === 'due_today' ? 'warning' : 'accent';
          return (
            <li key={r.taskId}>
              <a
                href={`/tasks/${r.taskId}`}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-2)',
                  color: 'var(--nf-color-fg)',
                  textDecoration: 'none',
                  fontSize: 'var(--nf-text-xs)',
                }}
              >
                <Badge tone={tone}>{t(`reminders.kind.${r.kind}`)}</Badge>
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {r.title}
                </span>
                <span
                  style={{
                    fontSize: 'var(--nf-text-micro)',
                    color: 'var(--nf-color-fg-muted)',
                  }}
                >
                  {r.dueOn ? formatRelativeDate(r.dueOn, zone, t) : ''}
                </span>
              </a>
            </li>
          );
        })}
      </ul>
      {workspaceId ? (
        <Link
          to="/workspaces/$id/reminders"
          params={{ id: workspaceId }}
          style={{
            alignSelf: 'flex-end',
            padding: 'var(--nf-space-1) var(--nf-space-2)',
            borderRadius: 'var(--nf-radius-sm)',
            color: 'var(--nf-color-fg-muted)',
            textDecoration: 'none',
            fontSize: 'var(--nf-text-micro)',
            fontWeight: 500,
          }}
        >
          {t('reminders.view_all')}
        </Link>
      ) : null}
    </div>
  );
}

function AutoActionsPanel({
  workspaceId,
}: {
  workspaceId: string | undefined;
}): ReactElement | null {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useAutoActionsQuery(workspaceId);
  const items: TaskAutoAction[] = data ?? [];
  if (items.length === 0) return null;
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-1-5)',
        padding: 'var(--nf-space-3)',
        borderBlockEnd: '1px solid var(--nf-color-border)',
      }}
    >
      <strong style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-muted)' }}>
        {t('auto_actions.title')}
      </strong>
      <p className={styles.fieldHint}>{t('auto_actions.hint')}</p>
      <ul
        style={{
          listStyle: 'none',
          padding: 0,
          margin: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--nf-space-1-5)',
        }}
      >
        {items.slice(0, 5).map((a) => {
          const tone =
            a.kind === 'escalate_overdue'
              ? 'danger'
              : a.kind === 'assign_owner'
                ? 'warning'
                : 'accent';
          return (
            <li key={`${a.taskId}-${a.kind}`}>
              <Link
                to="/tasks/$taskId"
                params={{ taskId: a.taskId }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--nf-space-2)',
                  color: 'var(--nf-color-fg)',
                  textDecoration: 'none',
                  fontSize: 'var(--nf-text-xs)',
                }}
              >
                <Badge tone={tone}>{t(`auto_actions.kind.${a.kind}`)}</Badge>
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {a.title}
                </span>
                <Badge
                  tone={
                    a.confidence >= 0.8 ? 'danger' : a.confidence >= 0.5 ? 'warning' : 'neutral'
                  }
                >
                  {confidenceLabel(a.confidence, t)}
                </Badge>
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function GlassDockImpl(): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const { t: tCommon } = useTranslation('common');
  const { t: tInbox } = useTranslation('inbox');
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [editingSuggestion, setEditingSuggestion] = useState<Suggestion | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const workspaceId = useCurrentWorkspaceId() ?? undefined;
  const actorId = useAuth(selectUser)?.id;
  useWorkspaceStream(workspaceId);
  const { data, isError: suggestionsFailed } = useAiSuggestionsQuery(workspaceId);
  const suggestions: Suggestion[] =
    workspaceId && actorId
      ? (data ?? []).map((suggestion) => ({ ...suggestion, workspaceId, actorId }))
      : [];
  // A resolved row stays mounted for its exit; the empty state waits for it.
  const suggestionPresence = usePresenceList(suggestions, (s) => s.inboxItemId);
  const suggestionAction = useSuggestionActionExecutor(workspaceId, (taskId) =>
    navigate({ to: '/tasks/$taskId', params: { taskId } }),
  );
  const pendingInboxItemId = suggestionAction.pendingInboxItemId;

  // biome-ignore lint/correctness/useExhaustiveDependencies: Reset the draft when the workspace or actor changes.
  useEffect(() => {
    setEditingSuggestion(null);
  }, [workspaceId, actorId]);

  useFocusTrap(panelRef, open);

  // Set when the panel closes so the pill that replaces it takes focus back.
  const restoreFocusRef = useRef(false);

  const handleToggle = (): void => {
    restoreFocusRef.current = open;
    if (open) setEditingSuggestion(null);
    setOpen((prev) => !prev);
  };

  const handleApply = (suggestion: Suggestion): void => {
    void suggestionAction.execute(suggestion).then((result) => {
      if (result.status !== 'executed') return;
      if (result.action === 'archive') {
        toaster.show({ tone: 'success', message: tInbox('toast.archived') });
      } else if (result.action === 'snooze') {
        toaster.show({ tone: 'success', message: tInbox('toast.snoozed') });
      }
    });
  };

  const handleEdit = (suggestion: Suggestion): void => {
    setEditingSuggestion(suggestion);
  };

  const handleSaveEdit = async (patch: EditSuggestionPatch): Promise<void> => {
    if (!editingSuggestion) return;
    if (await suggestionAction.saveEdit(editingSuggestion, patch)) {
      setEditingSuggestion(null);
    }
  };

  const handleDismiss = (suggestion: Suggestion): void => {
    void suggestionAction.dismiss(suggestion);
  };

  const countBadge =
    suggestions.length > 0 ? (
      <span key={suggestions.length} className={styles.count}>
        <Badge tone="accent">{suggestions.length}</Badge>
      </span>
    ) : null;

  if (!open) {
    return (
      <button
        ref={(el) => {
          if (el && restoreFocusRef.current) {
            restoreFocusRef.current = false;
            el.focus();
          }
        }}
        type="button"
        onClick={handleToggle}
        aria-expanded={false}
        aria-label={t('glass_dock.expand')}
        className={cx(styles.pill, 'nf-focus-ring')}
      >
        <Icon icon={Sparkles} decorative />
        <span className={styles.pillLabel}>{t('glass_dock.title')}</span>
        {countBadge}
      </button>
    );
  }

  return (
    <div
      ref={panelRef}
      role="region"
      aria-label={t('glass_dock.title')}
      className={styles.panel}
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation();
          restoreFocusRef.current = true;
          setOpen(false);
        }
      }}
    >
      <div className={styles.header}>
        <div className={styles.headerTitle}>
          <Icon icon={Sparkles} decorative />
          <strong>{t('glass_dock.title')}</strong>
          {countBadge}
          <PageHelp topic="ai-suggestions-panel" />
        </div>
        <button
          type="button"
          onClick={handleToggle}
          aria-expanded={true}
          aria-label={t('glass_dock.collapse')}
          className={cx(styles.close, 'nf-focus-ring')}
        >
          <Icon icon={X} decorative />
        </button>
      </div>
      <div className={styles.body}>
        <p className={styles.description}>{t('glass_dock.description')}</p>
        <NlQueryPanel workspaceId={workspaceId} />
        <AutoActionsPanel workspaceId={workspaceId} />
        <RemindersPanel workspaceId={workspaceId} />
        <StateSuggestionsPanel workspaceId={workspaceId} />
        <div className={styles.list}>
          {suggestionsFailed ? (
            <p key="unavailable" role="status" className={styles.message}>
              {tCommon('glass_dock.unavailable')}
            </p>
          ) : suggestionPresence.entries.length === 0 ? (
            <p key="empty" className={styles.message}>
              {t('glass_dock.empty')}
            </p>
          ) : (
            suggestionPresence.entries.map(({ key, item, present }) => (
              <PresenceItem
                key={key}
                present={present}
                onExited={() => suggestionPresence.onExited(key)}
              >
                <GlassDockSuggestionRow
                  suggestion={item}
                  disabled={pendingInboxItemId !== undefined || suggestionAction.decisionPending}
                  onApply={handleApply}
                  onEdit={handleEdit}
                  onDismiss={handleDismiss}
                />
              </PresenceItem>
            ))
          )}
        </div>
      </div>
      <EditSuggestionDialog
        suggestion={editingSuggestion}
        open={editingSuggestion !== null}
        onClose={() => setEditingSuggestion(null)}
        onSave={handleSaveEdit}
        saving={suggestionAction.decisionPending}
      />
    </div>
  );
}

/**
 * GlassDock — default export wraps the real implementation in a local
 * ErrorBoundary. The dock is decorative; if anything inside throws
 * synchronously (a sibling hook blows up, a query escalates past the
 * per-query `throwOnError: false` opt-out, etc.) the dock silently
 * disappears instead of collapsing the entire authenticated route to
 * the root FatalFallback.
 */
export default function GlassDock(): ReactElement {
  return (
    <ErrorBoundary fallback={null}>
      <GlassDockImpl />
    </ErrorBoundary>
  );
}
