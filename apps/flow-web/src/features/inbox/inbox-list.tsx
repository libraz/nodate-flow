/**
 * InboxList — renders the caller's inbox items via `useInboxQuery` and wires
 * archive / snooze mutations through to row callbacks. Includes a workspace
 * filter select that narrows the visible items client-side, and an
 * AI triage button that requests inbox suggestions for the active workspace.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useNavigate } from '@tanstack/react-router';
import { Inbox as InboxIcon } from 'lucide-react';
import { type ChangeEvent, type ReactElement, useState } from 'react';
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

  const workspaceFiltered =
    selectedWorkspaceId === 'all'
      ? items
      : items.filter((item) => item.workspaceId === selectedWorkspaceId);
  const filteredItems = handoffOnly
    ? workspaceFiltered.filter((item) => item.kind === HANDOFF_KIND)
    : workspaceFiltered;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-4)' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          gap: 'var(--nf-space-3)',
          flexWrap: 'wrap',
        }}
      >
        <label
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-1-5)',
            fontSize: 'var(--nf-text-sm)',
            color: 'var(--nf-color-fg-muted)',
          }}
        >
          {t('filter.workspace_label')}
          <select
            value={selectedWorkspaceId}
            onChange={handleWorkspaceChange}
            style={{
              padding: 'var(--nf-space-2) var(--nf-space-3)',
              borderRadius: 'var(--nf-radius-md)',
              border: '1px solid var(--nf-color-border)',
              background: 'var(--nf-color-surface)',
              color: 'var(--nf-color-fg)',
              fontSize: 'var(--nf-text-base)',
              // nf-token-override: component dimension, not a spacing step
              maxInlineSize: '20rem',
            }}
          >
            <option value="all">{t('filter.workspace_all')}</option>
            {workspaces.map((workspace) => (
              <option key={workspace.id} value={workspace.id}>
                {workspace.name}
              </option>
            ))}
          </select>
        </label>
        <Button
          type="button"
          variant={handoffOnly ? 'primary' : 'default'}
          size="sm"
          aria-pressed={handoffOnly}
          onClick={() => {
            setHandoffOnly((prev) => !prev);
          }}
        >
          {t('filter.handoff')}
        </Button>
        <Button
          type="button"
          variant="primary"
          size="sm"
          onClick={handleTriageClick}
          disabled={!triageWorkspaceId || triage.isPending || items.length === 0}
        >
          {triage.isPending ? tAi('triage.running') : tAi('triage.trigger')}
        </Button>
      </div>

      {suggestions.length > 0 ? (
        <section
          aria-label={tAi('triage.result_title')}
          style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}
        >
          <h2
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-sm)',
              fontWeight: 600,
              color: 'var(--nf-color-fg-muted)',
              textTransform: 'uppercase',
              letterSpacing: '0.04em',
            }}
          >
            {tAi('triage.result_title')}
          </h2>
          <ul
            style={{
              listStyle: 'none',
              padding: 0,
              margin: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--nf-space-2)',
            }}
          >
            {suggestions.map((suggestion) => (
              <li key={suggestion.inboxItemId}>
                <TriageSuggestionRow
                  suggestion={suggestion}
                  onApply={handleApplySuggestion}
                  onDismiss={handleDismissSuggestion}
                  onEdit={handleEditSuggestion}
                />
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {filteredItems.length === 0 ? (
        <div
          style={{
            padding: 'var(--nf-space-12) var(--nf-space-4)',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted)',
            border: '1px dashed var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-lg)',
            background: 'var(--nf-color-bg-sunken)',
          }}
        >
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 'var(--nf-space-3)',
            }}
          >
            <InboxIcon
              size={48}
              strokeWidth={1}
              style={{ color: 'var(--nf-color-fg-muted)', opacity: 0.5 }}
            />
            {t('view.empty')}
          </div>
        </div>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: 'var(--nf-space-3)',
          }}
        >
          {filteredItems.map((item) => (
            <li key={item.id}>
              <InboxItemRow item={item} onArchive={handleArchive} onSnooze={handleSnooze} />
            </li>
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
