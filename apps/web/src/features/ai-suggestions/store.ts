/**
 * Zustand store for AI suggestions across the app.
 *
 * Holds an in-memory list of inbox triage suggestions, deduped by
 * `inboxItemId`. No persistence — there is no GET endpoint yet, so the
 * inbox triage mutation is the sole producer and the Glass Dock + inbox
 * UI are consumers.
 */

import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';

import type { components } from '@nodate-flow/sdk';

/** Suggestion DTO mirrored from the SDK. */
export type Suggestion = components['schemas']['InboxTriageSuggestion'];

export interface SuggestionsState {
  suggestions: Suggestion[];
  /** Add or replace suggestions, deduping by inboxItemId (newer wins). */
  pushSuggestions: (next: Suggestion[]) => void;
  /** Remove a single suggestion by inboxItemId. */
  dismissSuggestion: (inboxItemId: string) => void;
  /** Patch a single suggestion in place (local-only edits). */
  updateSuggestion: (
    inboxItemId: string,
    patch: { recommendedAction: string; reasoning: string },
  ) => void;
  /** Clear all suggestions. */
  clear: () => void;
}

function dedupe(existing: Suggestion[], incoming: Suggestion[]): Suggestion[] {
  const map = new Map<string, Suggestion>();
  for (const s of existing) map.set(s.inboxItemId, s);
  for (const s of incoming) map.set(s.inboxItemId, s);
  return Array.from(map.values());
}

/** Vanilla store so non-React callers (mutations) can write directly. */
export const suggestionsStore = createStore<SuggestionsState>((set) => ({
  suggestions: [],
  pushSuggestions: (next) => {
    set((state) => ({ suggestions: dedupe(state.suggestions, next) }));
  },
  dismissSuggestion: (inboxItemId) => {
    set((state) => ({
      suggestions: state.suggestions.filter((s) => s.inboxItemId !== inboxItemId),
    }));
  },
  updateSuggestion: (inboxItemId, patch) => {
    set((state) => ({
      suggestions: state.suggestions.map((s) =>
        s.inboxItemId === inboxItemId ? { ...s, ...patch } : s,
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
export const selectSuggestions = (s: SuggestionsState): Suggestion[] => s.suggestions;
export const selectSuggestionCount = (s: SuggestionsState): number => s.suggestions.length;
