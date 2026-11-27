/**
 * InboxList — renders the caller's inbox items via `useInboxQuery` and wires
 * archive / snooze mutations through to row callbacks. Includes a workspace
 * filter select that narrows the visible items client-side, and an
 * AI triage button that requests inbox suggestions for the active workspace.
 */

import { useFirstLoadStagger } from '@nodate-flow/ui/hooks/use-first-load-stagger';
import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Select from '@nodate-flow/ui/primitives/select';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import ToggleChip from '@nodate-flow/ui/primitives/toggle-chip';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useNavigate } from '@tanstack/react-router';
import { Inbox as InboxIcon } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useShallow } from 'zustand/react/shallow';
import { formatApiError } from '../../lib/api-error';
import AiConsequence, { AiSparkle } from '../ai-suggestions/ai-consequence';
import EditSuggestionDialog, {
  type EditSuggestionPatch,
} from '../ai-suggestions/edit-suggestion-dialog';
import { type Suggestion, selectSuggestionsFor, useSuggestions } from '../ai-suggestions/store';
import { useSuggestionActionExecutor } from '../ai-suggestions/use-suggestion-action-executor';
import { selectUser, useAuth } from '../auth/auth-store';
import { useWorkspacesQuery } from '../workspaces/api';
import { useArchiveInboxItem, useInboxQuery, useSnoozeInboxItem } from './api';
import styles from './inbox.module.css';
import InboxItemRow from './inbox-item-row';
import { SuggestionActorChangedError, useInboxTriageMutation } from './triage-api';
import TriageSuggestionRow from './triage-suggestion-row';

type WorkspaceFilter = string | 'all';

const HANDOFF_KIND = 'agent.task.handoff_to_user';

export default function InboxList(): ReactElement {
  const { t } = useTranslation('inbox');
  const { t: tAi } = useTranslation('ai-suggestions');
  const navigate = useNavigate();
  const { data: items } = useInboxQuery();
  const { data: workspaces } = useWorkspacesQuery();
  const actorId = useAuth(selectUser)?.id ?? '';
  const archive = useArchiveInboxItem();
  const snooze = useSnoozeInboxItem();
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<WorkspaceFilter>('all');
  const [handoffOnly, setHandoffOnly] = useState<boolean>(false);
  const [editingSuggestion, setEditingSuggestion] = useState<Suggestion | null>(null);
  // Triage is per workspace: with "All workspaces" it runs on the first one,
  // and the status line under the toolbar says so.
  const triageWorkspaceId =
    selectedWorkspaceId === 'all' ? (workspaces[0]?.id ?? '') : selectedWorkspaceId;
  const triageWorkspaceName = workspaces.find((ws) => ws.id === triageWorkspaceId)?.name ?? '';
  const triage = useInboxTriageMutation(triageWorkspaceId);
  const suggestionAction = useSuggestionActionExecutor(triageWorkspaceId || undefined, (taskId) =>
    navigate({ to: '/tasks/$taskId', params: { taskId } }),
  );
  const suggestions = useSuggestions(useShallow(selectSuggestionsFor(triageWorkspaceId, actorId)));
  // Both lists keep a removed row mounted for its exit; the filters apply to
  // the entries afterwards so the hook always sees the stable cached arrays.
  const itemPresence = usePresenceList(items, (item) => item.id);
  const suggestionPresence = usePresenceList(suggestions, (s) => s.inboxItemId);
  const stagger = useFirstLoadStagger();
  const workspaceSelectId = useId();
  const triageStatusId = useId();

  // biome-ignore lint/correctness/useExhaustiveDependencies: Reset the draft when the workspace or actor changes.
  useEffect(() => {
    setEditingSuggestion(null);
  }, [triageWorkspaceId, actorId]);

  const handleArchive = (id: string): void => {
    archive.mutate(id, {
      onSuccess: () => {
        toaster.show({ tone: 'success', message: t('toast.archived') });
      },
      onError: (err) => {
        toaster.show({ tone: 'danger', message: formatApiError(err, t, 'toast.error') });
      },
    });
  };

  const handleSnooze = (id: string, snoozeUntil: number): void => {
    snooze.mutate(
      { id, snoozeUntil },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('toast.snoozed') });
        },
        onError: (err) => {
          toaster.show({ tone: 'danger', message: formatApiError(err, t, 'toast.error') });
        },
      },
    );
  };

  const handleWorkspaceChange = (event: ChangeEvent<HTMLSelectElement>): void => {
    setSelectedWorkspaceId(event.target.value as WorkspaceFilter);
  };

  const handleTriageClick = (): void => {
    if (!triageWorkspaceId) return;
    triage.mutate(undefined, {
      onSuccess: (suggestions) => {
        toaster.show({
          tone: 'success',
          message:
            suggestions.length > 0
              ? tAi('triage.success', { count: suggestions.length })
              : tAi('triage.success_empty'),
        });
      },
      onError: (err) => {
        if (err instanceof SuggestionActorChangedError) {
          toaster.show({ tone: 'danger', message: tAi('glass_dock.errors.identity_changed') });
          return;
        }
        toaster.show({ tone: 'danger', message: formatApiError(err, tAi, 'error') });
      },
    });
  };

  const handleApplySuggestion = (suggestion: Suggestion): void => {
    void suggestionAction.execute(suggestion).then((result) => {
      if (result.status !== 'executed') return;
      if (result.action === 'archive') {
        toaster.show({ tone: 'success', message: t('toast.archived') });
      } else if (result.action === 'snooze') {
        toaster.show({ tone: 'success', message: t('toast.snoozed') });
      }
    });
  };

  const handleDismissSuggestion = (suggestion: Suggestion): void => {
    void suggestionAction.dismiss(suggestion);
  };

  const handleEditSuggestion = (suggestion: Suggestion): void => {
    setEditingSuggestion(suggestion);
  };

  const handleCloseEdit = (): void => {
    setEditingSuggestion(null);
  };

  const handleSaveEdit = async (patch: EditSuggestionPatch): Promise<void> => {
    if (!editingSuggestion) return;
    if (await suggestionAction.saveEdit(editingSuggestion, patch)) {
      setEditingSuggestion(null);
    }
  };

  const triageItemCount = items.filter((item) => item.workspaceId === triageWorkspaceId).length;
  const triageDisabled = !triageWorkspaceId || triage.isPending || triageItemCount === 0;
  let triageStatus: string;
  if (!triageWorkspaceId) {
    triageStatus = t('triage_status.no_workspace');
  } else if (triageItemCount === 0) {
    triageStatus = t('triage_status.nothing', { workspace: triageWorkspaceName });
  } else if (selectedWorkspaceId === 'all' && workspaces.length > 1) {
    triageStatus = t('triage_status.all', { workspace: triageWorkspaceName });
  } else {
    triageStatus = t('triage_status.one', { workspace: triageWorkspaceName });
  }

  const visibleEntries = itemPresence.entries.filter(
    ({ item }) =>
      (selectedWorkspaceId === 'all' || item.workspaceId === selectedWorkspaceId) &&
      (!handoffOnly || item.kind === HANDOFF_KIND),
  );

  // With nothing in the inbox the filter and triage controls have nothing to
  // act on, so only the empty state shows.
  const showToolbar = items.length > 0 || suggestionPresence.entries.length > 0;

  return (
    <div className={styles.body}>
      {showToolbar ? (
        <>
          <div className={styles.toolbar}>
            {workspaces.length > 1 && (
              <label className={styles.workspaceField} htmlFor={workspaceSelectId}>
                {t('filter.workspace_label')}
                <Select
                  id={workspaceSelectId}
                  value={selectedWorkspaceId}
                  onChange={handleWorkspaceChange}
                  className={styles.workspaceSelect}
                >
                  <option value="all">{t('filter.workspace_all')}</option>
                  {workspaces.map((workspace) => (
                    <option key={workspace.id} value={workspace.id}>
                      {workspace.name}
                    </option>
                  ))}
                </Select>
              </label>
            )}
            <Tooltip content={t('filter.handoff_hint')}>
              <ToggleChip pressed={handoffOnly} onPressedChange={setHandoffOnly}>
                {t('filter.handoff')}
              </ToggleChip>
            </Tooltip>
            <Tooltip content={tAi('triage.hint')}>
              <Button
                type="button"
                className={styles.toolbarEnd}
                onClick={handleTriageClick}
                disabled={triageDisabled}
                aria-busy={triage.isPending}
                aria-describedby={triageStatusId}
              >
                <AiSparkle kind="suggests" />
                {triage.isPending ? tAi('triage.running') : tAi('triage.trigger')}
              </Button>
            </Tooltip>
          </div>
          <p id={triageStatusId} className={styles.triageStatus}>
            {triageStatus}
          </p>
        </>
      ) : null}

      {suggestionPresence.entries.length > 0 ? (
        <section aria-label={tAi('triage.result_title')} className={styles.suggestions}>
          <div className={styles.sectionHead}>
            <h2 className={styles.sectionTitle}>{tAi('triage.result_title')}</h2>
            <AiConsequence kind="suggests" />
          </div>
          <p className={styles.sectionHint}>{tAi('triage.score_hint')}</p>
          <ul className={styles.list}>
            {suggestionPresence.entries.map((entry, index) => (
              <PresenceItem
                key={entry.key}
                as="li"
                present={entry.present}
                onExited={() => suggestionPresence.onExited(entry.key)}
                className={motion.enter}
                style={stagger(index)}
              >
                <TriageSuggestionRow
                  suggestion={entry.item}
                  onApply={handleApplySuggestion}
                  onDismiss={handleDismissSuggestion}
                  onEdit={handleEditSuggestion}
                  disabled={
                    suggestionAction.pendingInboxItemId !== undefined ||
                    suggestionAction.decisionPending
                  }
                />
              </PresenceItem>
            ))}
          </ul>
        </section>
      ) : null}

      {visibleEntries.length === 0 ? (
        <EmptyState
          className={styles.empty}
          icon={<InboxIcon size={40} strokeWidth={1.25} aria-hidden="true" />}
          title={t('view.empty')}
          titleAs="p"
          description={t('view.empty_description')}
        />
      ) : (
        <ul className={styles.list}>
          {visibleEntries.map((entry, index) => (
            <PresenceItem
              key={entry.key}
              as="li"
              present={entry.present}
              onExited={() => itemPresence.onExited(entry.key)}
              className={motion.enter}
              style={stagger(index)}
            >
              <InboxItemRow item={entry.item} onArchive={handleArchive} onSnooze={handleSnooze} />
            </PresenceItem>
          ))}
        </ul>
      )}

      <EditSuggestionDialog
        suggestion={editingSuggestion}
        open={editingSuggestion !== null}
        onClose={handleCloseEdit}
        onSave={handleSaveEdit}
        saving={suggestionAction.decisionPending}
      />
    </div>
  );
}
