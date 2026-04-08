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
