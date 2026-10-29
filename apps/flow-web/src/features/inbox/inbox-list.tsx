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
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { useNavigate } from '@tanstack/react-router';
import { Inbox as InboxIcon } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { useApplyAiSuggestion, useDismissAiSuggestion } from '../ai-suggestions/api';
import EditSuggestionDialog, {
  type EditSuggestionPatch,
} from '../ai-suggestions/edit-suggestion-dialog';
import {
  type Suggestion,
  selectSuggestions,
  suggestionsStore,
  useSuggestions,
} from '../ai-suggestions/store';
import { useWorkspacesQuery } from '../workspaces/api';
import { useArchiveInboxItem, useInboxQuery, useSnoozeInboxItem } from './api';
import styles from './inbox.module.css';
import InboxItemRow from './inbox-item-row';
import { useInboxTriageMutation } from './triage-api';
import TriageSuggestionRow from './triage-suggestion-row';

type WorkspaceFilter = string | 'all';

const SNOOZE_ONE_HOUR_SEC = 60 * 60;
const HANDOFF_KIND = 'agent.task.handoff_to_user';

export default function InboxList(): ReactElement {
  const { t } = useTranslation('inbox');
  const { t: tAi } = useTranslation('ai-suggestions');
  const navigate = useNavigate();
  const { data: items } = useInboxQuery();
  const { data: workspaces } = useWorkspacesQuery();
  const archive = useArchiveInboxItem();
  const snooze = useSnoozeInboxItem();
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<WorkspaceFilter>('all');
  const [handoffOnly, setHandoffOnly] = useState<boolean>(false);
  const [editingSuggestion, setEditingSuggestion] = useState<Suggestion | null>(null);
  const triageWorkspaceId =
    selectedWorkspaceId === 'all' ? (workspaces[0]?.id ?? '') : selectedWorkspaceId;
  const triage = useInboxTriageMutation(triageWorkspaceId);
  const applySuggestion = useApplyAiSuggestion(triageWorkspaceId);
  const dismissSuggestion = useDismissAiSuggestion(triageWorkspaceId);
  const suggestions = useSuggestions(selectSuggestions);
  // Both lists keep a removed row mounted for its exit; the filters apply to
  // the entries afterwards so the hook always sees the stable cached arrays.
  const itemPresence = usePresenceList(items, (item) => item.id);
  const suggestionPresence = usePresenceList(suggestions, (s) => s.inboxItemId);
  const stagger = useFirstLoadStagger();
  const workspaceSelectId = useId();

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
        toaster.show({ tone: 'danger', message: formatApiError(err, tAi, 'error') });
      },
    });
  };

  const recordApply = (inboxItemId: string): void => {
    if (!triageWorkspaceId) return;
    // error-toast-exempt: bookkeeping after the acted-on mutation already reported its outcome
    applySuggestion.mutate(inboxItemId);
  };

  const handleApplySuggestion = (suggestion: Suggestion): void => {
    const item = items.find((it) => it.id === suggestion.inboxItemId);
    if (!item) {
      toaster.show({ tone: 'danger', message: tAi('triage.missing_item') });
      return;
    }
    const action = suggestion.recommendedAction;
    if (action === 'archive') {
      archive.mutate(item.id, {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('toast.archived') });
          recordApply(suggestion.inboxItemId);
        },
        onError: (err) => {
          toaster.show({ tone: 'danger', message: formatApiError(err, t, 'toast.error') });
        },
      });
    } else if (action === 'snooze') {
      const until = Math.floor(Date.now() / 1000) + SNOOZE_ONE_HOUR_SEC;
      snooze.mutate(
        { id: item.id, snoozeUntil: until },
        {
          onSuccess: () => {
            toaster.show({ tone: 'success', message: t('toast.snoozed') });
            recordApply(suggestion.inboxItemId);
          },
          onError: (err) => {
            toaster.show({ tone: 'danger', message: formatApiError(err, t, 'toast.error') });
          },
        },
      );
    } else if (action === 'open' && item.taskId) {
      void navigate({ to: '/tasks/$taskId', params: { taskId: item.taskId } });
      recordApply(suggestion.inboxItemId);
    } else {
      recordApply(suggestion.inboxItemId);
    }
    suggestionsStore.getState().dismissSuggestion(suggestion.inboxItemId);
  };

  const handleDismissSuggestion = (suggestion: Suggestion): void => {
    suggestionsStore.getState().dismissSuggestion(suggestion.inboxItemId);
    if (triageWorkspaceId) {
      // error-toast-exempt: bookkeeping only; the suggestion is already gone from the local store
      dismissSuggestion.mutate(suggestion.inboxItemId);
    }
  };

  const handleEditSuggestion = (suggestion: Suggestion): void => {
    setEditingSuggestion(suggestion);
  };

  const handleCloseEdit = (): void => {
    setEditingSuggestion(null);
  };

  const handleSaveEdit = (patch: EditSuggestionPatch): void => {
    if (!editingSuggestion) return;
    suggestionsStore.getState().updateSuggestion(editingSuggestion.inboxItemId, patch);
    setEditingSuggestion(null);
  };

  const visibleEntries = itemPresence.entries.filter(
    ({ item }) =>
      (selectedWorkspaceId === 'all' || item.workspaceId === selectedWorkspaceId) &&
      (!handoffOnly || item.kind === HANDOFF_KIND),
  );

  return (
    <div className={styles.body}>
      <div className={styles.toolbar}>
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
        <ToggleChip pressed={handoffOnly} onPressedChange={setHandoffOnly}>
          {t('filter.handoff')}
        </ToggleChip>
        <Button
          type="button"
          variant="primary"
          size="sm"
          className={styles.toolbarEnd}
          onClick={handleTriageClick}
          disabled={!triageWorkspaceId || triage.isPending || items.length === 0}
          aria-busy={triage.isPending}
        >
          {triage.isPending ? tAi('triage.running') : tAi('triage.trigger')}
        </Button>
      </div>

      {suggestionPresence.entries.length > 0 ? (
        <section aria-label={tAi('triage.result_title')} className={styles.suggestions}>
          <h2 className={styles.sectionTitle}>{tAi('triage.result_title')}</h2>
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
      />
    </div>
  );
}
