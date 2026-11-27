/**
 * AgentToolsEditor — the "what this agent may do on tasks" grants of one
 * task agent: five operations, each Off / Approval / Auto.
 *
 * The PATCH replaces the grants wholesale, so edits stay in a local draft
 * and are stored by one explicit save. An operation set to Off is left out
 * of the payload; all-Off is `{}`, which the runtime reads as "nothing
 * allowed", and is called out in the collapsed summary as well as inside.
 * Non-admins see the same layout with the controls disabled.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { ChevronDown } from 'lucide-react';
import { type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import SaveStatus, { useSaveStatus } from '../settings/save-status';
import styles from './agent-tools-editor.module.css';
import { type AgentSummary, useUpdateAgentTools } from './agents-api';

type GrantMode = 'off' | 'approval' | 'auto';

const OPERATIONS = [
  {
    name: 'add_comment',
    labelKey: 'agents.tools.operations.add_comment.label',
    descriptionKey: 'agents.tools.operations.add_comment.description',
  },
  {
    name: 'set_priority',
    labelKey: 'agents.tools.operations.set_priority.label',
    descriptionKey: 'agents.tools.operations.set_priority.description',
  },
  {
    name: 'set_due_on',
    labelKey: 'agents.tools.operations.set_due_on.label',
    descriptionKey: 'agents.tools.operations.set_due_on.description',
  },
  {
    name: 'transition_task',
    labelKey: 'agents.tools.operations.transition_task.label',
    descriptionKey: 'agents.tools.operations.transition_task.description',
  },
  {
    name: 'create_subtask',
    labelKey: 'agents.tools.operations.create_subtask.label',
    descriptionKey: 'agents.tools.operations.create_subtask.description',
  },
] as const;

type Draft = Readonly<Record<string, GrantMode>>;

function modeOf(draft: Draft, name: string): GrantMode {
  return draft[name] ?? 'off';
}

function toDraft(tools: AgentSummary['tools']): Draft {
  const draft: Record<string, GrantMode> = {};
  for (const op of OPERATIONS) {
    const value = tools?.[op.name];
    draft[op.name] = value === 'auto' || value === 'approval' ? value : 'off';
  }
  return draft;
}

function toPayload(draft: Draft): Record<string, 'auto' | 'approval'> {
  const out: Record<string, 'auto' | 'approval'> = {};
  for (const op of OPERATIONS) {
    const mode = modeOf(draft, op.name);
    if (mode !== 'off') out[op.name] = mode;
  }
  return out;
}

export interface AgentToolsEditorProps {
  workspaceId: string;
  agentId: string;
  /** Stored grants, as returned by the agent list. */
  tools: AgentSummary['tools'];
  /** Workspace admins edit; everyone else sees the same layout disabled. */
  canEdit: boolean;
}

export default function AgentToolsEditor({
  workspaceId,
  agentId,
  tools,
  canEdit,
}: AgentToolsEditorProps): ReactElement {
  const { t } = useTranslation('ai-suggestions');
  const update = useUpdateAgentTools();
  const [open, setOpen] = useState(false);
  const [baseline, setBaseline] = useState<Draft>(() => toDraft(tools));
  const [draft, setDraft] = useState<Draft>(baseline);
  const regionId = useId();

  const dirty = OPERATIONS.some((op) => modeOf(draft, op.name) !== modeOf(baseline, op.name));
  const saveStatus = useSaveStatus(dirty);
  const modes = OPERATIONS.map((op) => modeOf(baseline, op.name));
  const approvalCount = modes.filter((m) => m === 'approval').length;
  const autoCount = modes.filter((m) => m === 'auto').length;
  const nothingAllowed = approvalCount + autoCount === 0;
  const draftEmpty = OPERATIONS.every((op) => modeOf(draft, op.name) === 'off');

  const handleSave = async (): Promise<void> => {
    try {
      await update.mutateAsync({ workspaceId, agentId, tools: toPayload(draft) });
      setBaseline(draft);
      saveStatus.markSaved();
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'agents.tools.saveFailed') });
    }
  };

  return (
    <section className={styles.root} aria-label={t('agents.tools.title')}>
      <button
        type="button"
        className={styles.toggle}
        aria-expanded={open}
        aria-controls={regionId}
        onClick={() => {
          setOpen((v) => !v);
        }}
      >
        <ChevronDown size={16} aria-hidden className={styles.chevron} data-open={open} />
        <span className={styles.toggleTitle}>{t('agents.tools.title')}</span>
        {nothingAllowed ? (
          <Badge tone="warning">{t('agents.tools.inactive')}</Badge>
        ) : (
          <span className={styles.summary}>
            {t('agents.tools.summary', { approval: approvalCount, auto: autoCount })}
          </span>
        )}
      </button>
      <Collapse open={open} id={regionId}>
        <div className={styles.body}>
          <p className={styles.legend}>{t('agents.tools.legend')}</p>
          {nothingAllowed ? (
            <p className={styles.notice} role="note">
              {t('agents.tools.emptyWarning')}
            </p>
          ) : null}
          <ul className={styles.list}>
            {OPERATIONS.map((op) => {
              const labelId = `${regionId}-${op.name}-label`;
              const descId = `${regionId}-${op.name}-desc`;
              return (
                <li key={op.name} className={styles.row}>
                  <div className={styles.rowText}>
                    <span id={labelId} className={styles.rowLabel}>
                      {t(op.labelKey)}
                    </span>
                    <span id={descId} className={styles.rowDescription}>
                      {t(op.descriptionKey)}
                    </span>
                  </div>
                  <SegmentedControl<GrantMode>
                    size="sm"
                    ariaLabel={t(op.labelKey)}
                    value={modeOf(draft, op.name)}
                    disabled={!canEdit || update.isPending}
                    className={styles.control}
                    options={[
                      { value: 'off', label: t('agents.tools.modes.off') },
                      { value: 'approval', label: t('agents.tools.modes.approval') },
                      { value: 'auto', label: t('agents.tools.modes.auto') },
                    ]}
                    onChange={(next) => {
                      setDraft((prev) => ({ ...prev, [op.name]: next }));
                    }}
                  />
                </li>
              );
            })}
          </ul>
          {canEdit ? (
            <div className={styles.footer}>
              {dirty && draftEmpty ? (
                <span className={styles.footerWarning}>{t('agents.tools.emptyWarning')}</span>
              ) : null}
              <SaveStatus saved={saveStatus.saved} dirty={dirty} />
              <Button
                type="button"
                size="sm"
                variant="primary"
                disabled={!dirty || update.isPending}
                aria-busy={update.isPending}
                onClick={() => {
                  void handleSave();
                }}
              >
                {t('agents.tools.save')}
              </Button>
            </div>
          ) : (
            <p className={styles.readOnly}>{t('agents.tools.readOnly')}</p>
          )}
        </div>
      </Collapse>
    </section>
  );
}
