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
import Icon from '@nodate-flow/ui/icon';
import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import { useMatches } from '@tanstack/react-router';
import { Sparkles, X } from 'lucide-react';
import { type ReactElement, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type AiSuggestion,
  useAiSuggestionsQuery,
  useApplyAiSuggestion,
  useDismissAiSuggestion,
} from '../ai-suggestions/api';
import SuggestionActions from '../ai-suggestions/suggestion-actions';
import { type CompileLensResult, NlQueryError, useCompileLens } from '../nl-query/api';
import { type StateSuggestion, useStateSuggestionsQuery } from '../nl-query/state-suggestions';
import { type TaskReminder, useRemindersQuery } from '../reminders/api';

const NL_UNPARSEABLE = 'AI.NL_QUERY.UNPARSEABLE';

function NlQueryPanel({ workspaceId }: { workspaceId: string | undefined }): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const [prompt, setPrompt] = useState('');
  const [result, setResult] = useState<CompileLensResult | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const mutation = useCompileLens();

  const disabled = !workspaceId || prompt.trim().length === 0 || mutation.isPending;

  const handleSubmit = (event: React.FormEvent): void => {
    event.preventDefault();
    if (!workspaceId || prompt.trim().length === 0) return;
    setErrorMsg(null);
    setResult(null);
    mutation.mutate(
      { workspaceId, prompt: prompt.trim() },
      {
        onSuccess: (r) => setResult(r),
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

  return (
    <form
      onSubmit={handleSubmit}
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '0.5rem',
        padding: '0.75rem',
        borderBlockEnd: '1px solid var(--color-border)',
      }}
    >
      <label htmlFor="nl-query-input" style={{ fontSize: '0.75rem', color: 'var(--color-muted)' }}>
        {t('nl_query.label')}
      </label>
      <input
        id="nl-query-input"
        type="text"
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        placeholder={t('nl_query.placeholder')}
        maxLength={500}
        style={{
          padding: '0.5rem 0.625rem',
          borderRadius: '0.375rem',
          border: '1px solid var(--color-border)',
          background: 'var(--color-bg)',
          color: 'var(--color-fg)',
          fontSize: '0.8125rem',
        }}
      />
      <button
        type="submit"
        disabled={disabled}
        style={{
          padding: '0.4rem 0.75rem',
          borderRadius: '0.375rem',
          border: '1px solid var(--color-border)',
          background: 'var(--color-surface)',
          color: 'var(--color-fg)',
          fontSize: '0.75rem',
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.6 : 1,
        }}
      >
        {mutation.isPending ? t('nl_query.compiling') : t('nl_query.submit')}
      </button>
      {errorMsg ? (
        <p role="alert" style={{ margin: 0, fontSize: '0.75rem', color: 'var(--color-danger)' }}>
          {errorMsg}
        </p>
      ) : null}
      {result ? (
        <pre
          style={{
            margin: 0,
            padding: '0.5rem',
            background: 'var(--color-bg)',
            border: '1px solid var(--color-border)',
            borderRadius: '0.375rem',
            fontSize: '0.6875rem',
            color: 'var(--color-fg)',
            overflow: 'auto',
            maxBlockSize: '8rem',
          }}
        >
          {JSON.stringify(result.lens, null, 2)}
        </pre>
      ) : null}
    </form>
  );
}

function useActiveWorkspaceId(): string | undefined {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i -= 1) {
    const params = matches[i]?.params as Record<string, string> | undefined;
    if (!params) continue;
    const id = params.id ?? params.wsId;
    if (typeof id === 'string' && id.length > 0) return id;
  }
  return undefined;
}

function GlassDockSuggestionRow({
  suggestion,
  onApply,
  onDismiss,
}: {
  suggestion: AiSuggestion;
  onApply: (inboxItemId: string) => void;
  onDismiss: (inboxItemId: string) => void;
}): ReactElement {
  const handleApply = (): void => {
    onApply(suggestion.inboxItemId);
  };
  const handleDismiss = (): void => {
    onDismiss(suggestion.inboxItemId);
  };
  return (
    <Card style={{ padding: '0.625rem 0.75rem' }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <Badge
            tone={
              suggestion.score >= 0.8 ? 'danger' : suggestion.score >= 0.5 ? 'warning' : 'neutral'
            }
          >
            {suggestion.score.toFixed(2)}
          </Badge>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: '0.75rem',
              color: 'var(--color-muted)',
            }}
          >
            {suggestion.recommendedAction}
          </span>
        </div>
        <p
          style={{
            margin: 0,
            fontSize: '0.8125rem',
            color: 'var(--color-fg)',
            overflow: 'hidden',
            display: '-webkit-box',
            // biome-ignore lint/style/useNamingConvention: vendor-prefixed CSS props
            WebkitLineClamp: 2,
            // biome-ignore lint/style/useNamingConvention: vendor-prefixed CSS props
            WebkitBoxOrient: 'vertical',
          }}
        >
          {suggestion.reasoning}
        </p>
        <SuggestionActions
          onApply={handleApply}
          onDismiss={handleDismiss}
          onEdit={() => {
            /* edit modal — wave 1 follow-up */
          }}
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
        gap: '0.375rem',
        padding: '0.75rem',
        borderBlockEnd: '1px solid var(--color-border)',
      }}
    >
      <strong style={{ fontSize: '0.75rem', color: 'var(--color-muted)' }}>
        {t('state_suggestions.title')}
      </strong>
      <ul
        style={{
          listStyle: 'none',
          padding: 0,
          margin: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.375rem',
        }}
      >
        {items.slice(0, 5).map((s) => (
          <li key={s.taskId}>
            <a
              href={`/tasks/${s.taskId}`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.5rem',
                color: 'var(--color-fg)',
                textDecoration: 'none',
                fontSize: '0.75rem',
              }}
            >
              <Badge tone="accent">{s.transition}</Badge>
              <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {s.title}
              </span>
              <span
                style={{
                  fontFamily: 'var(--font-mono)',
                  color: 'var(--color-muted)',
                }}
              >
                {s.confidence.toFixed(2)}
              </span>
            </a>
          </li>
        ))}
      </ul>
    </div>
  );
}

function RemindersPanel({
  workspaceId,
}: {
  workspaceId: string | undefined;
}): ReactElement | null {
  const { t } = useTranslation('ai-suggestions');
  const { data } = useRemindersQuery(workspaceId);
  const items: TaskReminder[] = data ?? [];
  if (items.length === 0) return null;
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: '0.375rem',
        padding: '0.75rem',
        borderBlockEnd: '1px solid var(--color-border)',
      }}
    >
      <strong style={{ fontSize: '0.75rem', color: 'var(--color-muted)' }}>
        {t('reminders.title')}
      </strong>
      <ul
        style={{
          listStyle: 'none',
          padding: 0,
          margin: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.375rem',
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
                  gap: '0.5rem',
                  color: 'var(--color-fg)',
                  textDecoration: 'none',
                  fontSize: '0.75rem',
                }}
              >
                <Badge tone={tone}>{t(`reminders.kind.${r.kind}`)}</Badge>
                <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {r.title}
                </span>
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    color: 'var(--color-muted)',
                  }}
                >
                  {r.dueOn}
                </span>
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export default function GlassDock(): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const [open, setOpen] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);

  const workspaceId = useActiveWorkspaceId();
  const { data } = useAiSuggestionsQuery(workspaceId);
  const suggestions: AiSuggestion[] = data ?? [];
  const applyMutation = useApplyAiSuggestion(workspaceId ?? '');
  const dismissMutation = useDismissAiSuggestion(workspaceId ?? '');

  useFocusTrap(panelRef, open);

  const handleToggle = (): void => {
    setOpen((prev) => !prev);
  };

  const handleApply = (inboxItemId: string): void => {
    if (!workspaceId) return;
    applyMutation.mutate(inboxItemId);
  };

  const handleDismiss = (inboxItemId: string): void => {
    if (!workspaceId) return;
    dismissMutation.mutate(inboxItemId);
  };

  if (!open) {
    return (
      <button
        type="button"
        onClick={handleToggle}
        aria-expanded={false}
        aria-label={t('glass_dock.expand')}
        style={{
          position: 'fixed',
          insetBlockEnd: '1rem',
          insetInlineEnd: '1rem',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
          padding: '0.625rem 0.875rem',
          borderRadius: '999px',
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          color: 'var(--color-fg)',
          boxShadow: '0 8px 24px rgba(0, 0, 0, 0.12)',
          cursor: 'pointer',
          zIndex: 50,
        }}
      >
        <Icon icon={Sparkles} decorative />
        <span style={{ fontSize: '0.8125rem', fontWeight: 600 }}>{t('glass_dock.title')}</span>
        {suggestions.length > 0 ? <Badge tone="accent">{suggestions.length}</Badge> : null}
      </button>
    );
  }

  return (
    <div
      ref={panelRef}
      role="region"
      aria-label={t('glass_dock.title')}
      style={{
        position: 'fixed',
        insetBlockEnd: '1rem',
        insetInlineEnd: '1rem',
        inlineSize: '360px',
        maxBlockSize: '70vh',
        display: 'flex',
        flexDirection: 'column',
        background: 'var(--color-surface)',
        border: '1px solid var(--color-border)',
        borderRadius: '0.75rem',
        boxShadow: '0 16px 48px rgba(0, 0, 0, 0.18)',
        zIndex: 50,
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '0.75rem 0.875rem',
          borderBlockEnd: '1px solid var(--color-border)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
          <Icon icon={Sparkles} decorative />
          <strong style={{ fontSize: '0.875rem' }}>{t('glass_dock.title')}</strong>
          {suggestions.length > 0 ? <Badge tone="accent">{suggestions.length}</Badge> : null}
        </div>
        <button
          type="button"
          onClick={handleToggle}
          aria-expanded={true}
          aria-label={t('glass_dock.collapse')}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            inlineSize: '1.75rem',
            blockSize: '1.75rem',
            borderRadius: '0.375rem',
            border: 'none',
            background: 'transparent',
            color: 'var(--color-fg)',
            cursor: 'pointer',
          }}
        >
          <Icon icon={X} decorative />
        </button>
      </div>
      <NlQueryPanel workspaceId={workspaceId} />
      <RemindersPanel workspaceId={workspaceId} />
      <StateSuggestionsPanel workspaceId={workspaceId} />
      <div
        style={{
          overflow: 'auto',
          padding: '0.75rem',
          display: 'flex',
          flexDirection: 'column',
          gap: '0.5rem',
        }}
      >
        {suggestions.length === 0 ? (
          <p
            style={{
              margin: 0,
              color: 'var(--color-muted)',
              fontSize: '0.8125rem',
              textAlign: 'center',
              padding: '1rem',
            }}
          >
            {t('glass_dock.empty')}
          </p>
        ) : (
          suggestions.map((suggestion) => (
            <GlassDockSuggestionRow
              key={suggestion.inboxItemId}
              suggestion={suggestion}
              onApply={handleApply}
              onDismiss={handleDismiss}
            />
          ))
        )}
      </div>
    </div>
  );
}
