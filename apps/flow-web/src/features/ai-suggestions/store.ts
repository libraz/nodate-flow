/**
 * Short-lived cache of persisted suggestions shown inline after triage.
 * Server GET remains authoritative; every entry is scoped by workspace and
 * actor so a user or workspace switch cannot show another scope's edits.
 */

import type { components } from '@nodate-flow/sdk';
import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';

/** Suggestion DTO mirrored from the persisted AI suggestion list. */
export type AiSuggestion = components['schemas']['AiSuggestionSummary'];

/** Inline suggestion plus the local scope that fetched it. */
export type Suggestion = AiSuggestion & { workspaceId: string; actorId: string };

export interface SuggestionsState {
  suggestions: Suggestion[];
  /** Add or replace the current actor/workspace's persisted suggestions. */
  pushSuggestions: (next: Suggestion[]) => void;
  /** Remove one proposal only from the actor/workspace that acted on it. */
  dismissSuggestion: (proposalId: string, workspaceId: string, actorId: string) => void;
  /** Patch the visible persisted result after the server saved a decision. */
  updateSuggestion: (
    proposalId: string,
    workspaceId: string,
    actorId: string,
    patch: Partial<AiSuggestion>,
  ) => void;
  /** Clear the transient inline cache. */
  clear: () => void;
}

function scopeKey(suggestion: Suggestion): string {
  return `${suggestion.workspaceId}:${suggestion.actorId}:${suggestion.inboxItemId}`;
}

function dedupe(existing: Suggestion[], incoming: Suggestion[]): Suggestion[] {
  const map = new Map<string, Suggestion>();
  for (const suggestion of existing) map.set(scopeKey(suggestion), suggestion);
  for (const suggestion of incoming) map.set(scopeKey(suggestion), suggestion);
  return Array.from(map.values());
}

/** Vanilla store so mutation callbacks can write without React. */
export const suggestionsStore = createStore<SuggestionsState>((set) => ({
  suggestions: [],
  pushSuggestions: (next) => {
    set((state) => ({ suggestions: dedupe(state.suggestions, next) }));
  },
  dismissSuggestion: (proposalId, workspaceId, actorId) => {
    set((state) => ({
      suggestions: state.suggestions.filter(
        (suggestion) =>
          suggestion.proposalId !== proposalId ||
          suggestion.workspaceId !== workspaceId ||
          suggestion.actorId !== actorId,
      ),
    }));
  },
  updateSuggestion: (proposalId, workspaceId, actorId, patch) => {
    set((state) => ({
      suggestions: state.suggestions.map((suggestion) =>
        suggestion.proposalId === proposalId &&
        suggestion.workspaceId === workspaceId &&
        suggestion.actorId === actorId
          ? { ...suggestion, ...patch }
          : suggestion,
      ),
    }));
  },
  clear: () => {
    set({ suggestions: [] });
  },
}));

/** React hook with selector. Always pass a selector to avoid over-rendering. */
export function useSuggestions<T>(selector: (state: SuggestionsState) => T): T {
  return useStore(suggestionsStore, selector);
}

/** Convenience selectors. */
export const selectSuggestionsFor =
  (workspaceId: string, actorId: string) =>
  (state: SuggestionsState): Suggestion[] =>
    state.suggestions.filter(
      (suggestion) => suggestion.workspaceId === workspaceId && suggestion.actorId === actorId,
    );
export const selectSuggestionCount = (state: SuggestionsState): number => state.suggestions.length;
