/** Shared server-owned execution path for persisted AI inbox suggestions. */

import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useQueryClient } from '@tanstack/react-query';
import type { TFunction } from 'i18next';
import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError, formatApiError } from '../../lib/api-error';
import { authStore, selectUser, useAuth } from '../auth/auth-store';
import {
  type AiSuggestion,
  type AiSuggestionAction,
  aiSuggestionsKeys,
  fetchAiSuggestions,
  SuggestionActorChangedError,
  useExecuteAiSuggestion,
  useSaveAiSuggestionDecision,
} from './api';
import { suggestionsStore } from './store';

export type SuggestionActionInput = AiSuggestion & {
  workspaceId: string;
  actorId: string;
};

export type SuggestionActionOutcome =
  | { status: 'executed'; action: string }
  | { status: 'navigation-ready' }
  | { status: 'busy' }
  | { status: 'failed' }
  | { status: 'legacy-proposal' }
  | { status: 'missing-workspace' }
  | { status: 'identity-changed' }
  | { status: 'unsupported-action' };

export interface SuggestionActionExecutor {
  execute: (suggestion: SuggestionActionInput) => Promise<SuggestionActionOutcome>;
  dismiss: (suggestion: SuggestionActionInput) => Promise<boolean>;
  saveEdit: (
    suggestion: SuggestionActionInput,
    patch: { recommendedAction: AiSuggestionAction; reasoning: string },
  ) => Promise<boolean>;
  pendingInboxItemId: string | undefined;
  decisionPending: boolean;
}

function isSupportedAction(action: string): action is AiSuggestionAction {
  return action === 'archive' || action === 'snooze' || action === 'open';
}

function suggestionErrorMessage(
  error: unknown,
  t: TFunction<'ai-suggestions'>,
  fallbackKey: string,
): string {
  if (error instanceof SuggestionActorChangedError) return t('glass_dock.errors.identity_changed');
  if (error instanceof ApiError) {
    if (error.code === 'AI.SUGGESTION.EVIDENCE_CHANGED') return t('triage.source_changed');
    if (error.code === 'AI.SUGGESTION.ALREADY_RESOLVED') return t('triage.already_resolved');
  }
  return formatApiError(error, t, fallbackKey);
}

/**
 * Execute archive / snooze / open and save actor-scoped decisions through the
 * command API. Workspace, actor, proposal revision, and retry key are captured
 * before the first await and never read from changing hook options afterwards.
 */
export function useSuggestionActionExecutor(
  workspaceId: string | undefined,
  openTask: (taskId: string) => void | Promise<unknown>,
): SuggestionActionExecutor {
  const { t } = useTranslation('ai-suggestions');
  const user = useAuth(selectUser);
  const actorId = user?.id;
  const queryClient = useQueryClient();
  const executeMutation = useExecuteAiSuggestion();
  const decisionMutation = useSaveAiSuggestionDecision();
  const pendingIdRef = useRef<string | null>(null);
  const retryKeysRef = useRef(new Map<string, string>());
  const identityRef = useRef({ workspaceId, actorId });
  identityRef.current = { workspaceId, actorId };
  const [pendingInboxItemId, setPendingInboxItemId] = useState<string | undefined>();

  const isCurrentIdentity = (capturedWorkspace: string, capturedActor: string): boolean =>
    identityRef.current.workspaceId === capturedWorkspace &&
    identityRef.current.actorId === capturedActor &&
    authStore.getState().user?.id === capturedActor;

  const removeSuggestion = (proposalId: string, ws: string, actor: string): void => {
    queryClient.setQueryData<AiSuggestion[]>(aiSuggestionsKeys.list(ws, actor), (current) =>
      current?.filter((entry) => entry.proposalId !== proposalId),
    );
    suggestionsStore.getState().dismissSuggestion(proposalId, ws, actor);
  };

  const refreshInlineSuggestion = async (
    suggestion: SuggestionActionInput,
    ws: string,
    actor: string,
  ): Promise<void> => {
    let latest: AiSuggestion[];
    try {
      latest = await queryClient.fetchQuery({
        queryKey: aiSuggestionsKeys.list(ws, actor),
        queryFn: () => fetchAiSuggestions(ws),
        staleTime: 0,
      });
    } catch {
      return;
    }
    if (!isCurrentIdentity(ws, actor)) return;
    const fresh = latest.find((entry) => entry.inboxItemId === suggestion.inboxItemId);
    if (fresh) {
      suggestionsStore.getState().pushSuggestions([{ ...fresh, workspaceId: ws, actorId: actor }]);
    } else {
      suggestionsStore.getState().dismissSuggestion(suggestion.proposalId, ws, actor);
    }
  };

  /** Reconcile local copies after the server rejected a stale proposal. */
  const reconcileConflict = async (
    error: unknown,
    suggestion: SuggestionActionInput,
    ws: string,
    actor: string,
  ): Promise<void> => {
    if (!(error instanceof ApiError)) return;
    if (error.code === 'AI.SUGGESTION.ALREADY_RESOLVED') {
      removeSuggestion(suggestion.proposalId, ws, actor);
    } else if (error.code === 'AI.SUGGESTION.EVIDENCE_CHANGED') {
      await refreshInlineSuggestion(suggestion, ws, actor);
    }
  };

  const execute = async (suggestion: SuggestionActionInput): Promise<SuggestionActionOutcome> => {
    if (pendingIdRef.current !== null) return { status: 'busy' };
    if (!workspaceId || !actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return { status: 'missing-workspace' };
    }
    if (suggestion.workspaceId !== workspaceId || suggestion.actorId !== actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return { status: 'identity-changed' };
    }
    if (suggestion.sourceVersionStatus !== 'captured' || !suggestion.proposalId) {
      toaster.show({ tone: 'danger', message: t('triage.legacy_notice') });
      return { status: 'legacy-proposal' };
    }
    if (!isSupportedAction(suggestion.recommendedAction)) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.apply_failed') });
      return { status: 'unsupported-action' };
    }

    const capturedWorkspace = workspaceId;
    const capturedActor = actorId;
    const action = suggestion.recommendedAction;
    const requestFingerprint = JSON.stringify([
      capturedWorkspace,
      capturedActor,
      suggestion.inboxItemId,
      suggestion.proposalId,
      suggestion.revision,
      action,
      suggestion.reasoning,
    ]);
    const operationKey = retryKeysRef.current.get(requestFingerprint) ?? crypto.randomUUID();
    retryKeysRef.current.set(requestFingerprint, operationKey);
    pendingIdRef.current = suggestion.inboxItemId;
    setPendingInboxItemId(suggestion.inboxItemId);

    try {
      const result = await executeMutation.mutateAsync({
        workspaceId: capturedWorkspace,
        actorId: capturedActor,
        inboxItemId: suggestion.inboxItemId,
        proposalId: suggestion.proposalId,
        expectedRevision: suggestion.revision,
        operationKey,
        action,
        reasoning: suggestion.reasoning,
      });
      if (!isCurrentIdentity(capturedWorkspace, capturedActor)) {
        return { status: 'identity-changed' };
      }
      if (result.status === 'navigation_ready') {
        if (!result.taskId) {
          toaster.show({ tone: 'danger', message: t('glass_dock.errors.apply_failed') });
          return { status: 'failed' };
        }
        await openTask(result.taskId);
        if (!isCurrentIdentity(capturedWorkspace, capturedActor)) {
          return { status: 'identity-changed' };
        }
        retryKeysRef.current.delete(requestFingerprint);
        toaster.show({ tone: 'success', message: t('triage.opened') });
        return { status: 'navigation-ready' };
      }
      if (result.status !== 'executed') {
        toaster.show({ tone: 'danger', message: t('glass_dock.errors.apply_failed') });
        return { status: 'failed' };
      }
      retryKeysRef.current.delete(requestFingerprint);
      removeSuggestion(suggestion.proposalId, capturedWorkspace, capturedActor);
      return { status: 'executed', action: result.action };
    } catch (error) {
      if (error instanceof Error && error.name === 'SuggestionActorChangedError') {
        toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
        return { status: 'identity-changed' };
      }
      toaster.show({
        tone: 'danger',
        message: suggestionErrorMessage(error, t, 'glass_dock.errors.apply_failed'),
      });
      await reconcileConflict(error, suggestion, capturedWorkspace, capturedActor);
      return { status: 'failed' };
    } finally {
      pendingIdRef.current = null;
      setPendingInboxItemId(undefined);
    }
  };

  const saveEdit = async (
    suggestion: SuggestionActionInput,
    patch: { recommendedAction: AiSuggestionAction; reasoning: string },
  ): Promise<boolean> => {
    if (pendingIdRef.current !== null) return false;
    if (!workspaceId || !actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return false;
    }
    if (suggestion.sourceVersionStatus !== 'captured' || !suggestion.proposalId) {
      toaster.show({ tone: 'danger', message: t('triage.legacy_notice') });
      return false;
    }
    if (suggestion.workspaceId !== workspaceId || suggestion.actorId !== actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return false;
    }
    const capturedWorkspace = workspaceId;
    const capturedActor = actorId;
    if (!isCurrentIdentity(capturedWorkspace, capturedActor)) return false;
    pendingIdRef.current = suggestion.inboxItemId;
    try {
      const result = await decisionMutation.mutateAsync({
        workspaceId: capturedWorkspace,
        actorId: capturedActor,
        inboxItemId: suggestion.inboxItemId,
        proposalId: suggestion.proposalId,
        expectedRevision: suggestion.revision,
        state: 'edited',
        action: patch.recommendedAction,
        reasoning: patch.reasoning,
      });
      if (!isCurrentIdentity(capturedWorkspace, capturedActor)) return false;
      queryClient.setQueryData<AiSuggestion[]>(
        aiSuggestionsKeys.list(capturedWorkspace, capturedActor),
        (current) =>
          current?.map((entry) =>
            entry.proposalId === suggestion.proposalId
              ? {
                  ...entry,
                  revision: result.revision,
                  decisionState: result.state,
                  decisionExpiresAt: result.decisionExpiresAt,
                  recommendedAction: result.action ?? patch.recommendedAction,
                  reasoning: result.reasoning ?? patch.reasoning,
                }
              : entry,
          ),
      );
      suggestionsStore
        .getState()
        .updateSuggestion(suggestion.proposalId, capturedWorkspace, capturedActor, {
          revision: result.revision,
          decisionState: result.state,
          decisionExpiresAt: result.decisionExpiresAt,
          recommendedAction: result.action ?? patch.recommendedAction,
          reasoning: result.reasoning ?? patch.reasoning,
        });
      return true;
    } catch (error) {
      toaster.show({
        tone: 'danger',
        message: suggestionErrorMessage(error, t, 'glass_dock.errors.edit_failed'),
      });
      await reconcileConflict(error, suggestion, capturedWorkspace, capturedActor);
      return false;
    } finally {
      pendingIdRef.current = null;
    }
  };

  const dismiss = async (suggestion: SuggestionActionInput): Promise<boolean> => {
    if (pendingIdRef.current !== null) return false;
    if (!workspaceId || !actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return false;
    }
    if (suggestion.sourceVersionStatus !== 'captured' || !suggestion.proposalId) {
      toaster.show({ tone: 'danger', message: t('triage.legacy_notice') });
      return false;
    }
    if (suggestion.workspaceId !== workspaceId || suggestion.actorId !== actorId) {
      toaster.show({ tone: 'danger', message: t('glass_dock.errors.identity_changed') });
      return false;
    }
    const capturedWorkspace = workspaceId;
    const capturedActor = actorId;
    if (!isCurrentIdentity(capturedWorkspace, capturedActor)) return false;
    pendingIdRef.current = suggestion.inboxItemId;
    try {
      await decisionMutation.mutateAsync({
        workspaceId: capturedWorkspace,
        actorId: capturedActor,
        inboxItemId: suggestion.inboxItemId,
        proposalId: suggestion.proposalId,
        expectedRevision: suggestion.revision,
        state: 'dismissed',
      });
      if (!isCurrentIdentity(capturedWorkspace, capturedActor)) return false;
      removeSuggestion(suggestion.proposalId, capturedWorkspace, capturedActor);
      return true;
    } catch (error) {
      toaster.show({
        tone: 'danger',
        message: suggestionErrorMessage(error, t, 'glass_dock.errors.dismiss_failed'),
      });
      await reconcileConflict(error, suggestion, capturedWorkspace, capturedActor);
      return false;
    } finally {
      pendingIdRef.current = null;
    }
  };

  return {
    execute,
    dismiss,
    saveEdit,
    pendingInboxItemId,
    decisionPending: decisionMutation.isPending,
  };
}
