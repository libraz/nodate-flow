/**
 * TaskStepsPanel — collapsible panel for AI-powered task decomposition.
 *
 * Lets the user propose subtask steps via AI, review / edit / toggle them,
 * then apply selected steps as real subtasks.
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Input from '@nodate-flow/ui/primitives/input';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import {
  type StepGranularity,
  type StepProposalUI,
  useApplySteps,
  useProposeSteps,
} from './steps-api';

const PRIORITY_TONE: Record<string, BadgeTone> = {
  low: 'neutral',
  medium: 'warning',
  high: 'danger',
};

const PRIORITY_MAP: Record<string, number> = {
  low: 1,
  medium: 2,
  high: 3,
};

interface StepItemProps {
  step: StepProposalUI;
  index: number;
  checked: boolean;
  title: string;
  onToggle: (index: number) => void;
  onTitleChange: (index: number, value: string) => void;
}

function StepItem({
  step,
  index,
  checked,
  title,
  onToggle,
  onTitleChange,
}: StepItemProps): ReactElement {
  const { t } = useTranslation('common');
  const [expanded, setExpanded] = useState(false);
  const checkboxId = `step-check-${String(index)}`;

  return (
    <li
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-2)',
        padding: 'var(--nf-space-2) var(--nf-space-3)',
        borderRadius: 'var(--nf-radius-sm)',
        background: checked ? 'var(--nf-color-bg-sunken)' : 'transparent',
        opacity: checked ? 1 : 0.6,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
        <input
          id={checkboxId}
          type="checkbox"
          checked={checked}
          onChange={() => {
            onToggle(index);
          }}
          aria-label={t('tasks.steps.toggle_step', { title })}
        />
        <Input
          value={title}
          onChange={(e) => {
            onTitleChange(index, e.target.value);
          }}
          aria-label={t('tasks.steps.edit_title')}
          style={{ flex: 1, fontWeight: 500 }}
        />
        <Badge tone={PRIORITY_TONE[step.priority] ?? 'neutral'}>
          {t(`tasks.steps.priority_${step.priority}`)}
        </Badge>
      </div>
      {step.description.length > 0 ? (
        // Indent the description under the checkbox. The original 1.75rem
        // offset is reproduced as `space-6 + space-1` (1.5rem + 0.25rem)
        // so the value still flows through the spacing scale.
        <div style={{ marginInlineStart: 'calc(var(--nf-space-6) + var(--nf-space-1))' }}>
          <button
            type="button"
            onClick={() => {
              setExpanded((prev) => !prev);
            }}
            style={{
              background: 'none',
              border: 'none',
              padding: 0,
              cursor: 'pointer',
              color: 'var(--nf-color-fg-muted)',
              fontSize: 'var(--nf-text-xs)',
              textDecoration: 'underline',
            }}
          >
            {expanded ? t('tasks.steps.collapse') : t('tasks.steps.expand')}
          </button>
          {expanded ? (
            <p
              style={{
                margin: 'var(--nf-space-1) 0 0',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg)',
                whiteSpace: 'pre-wrap',
              }}
            >
              {step.description}
            </p>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}

interface TaskStepsPanelProps {
  taskId: string;
  workspaceId: string;
}

export default function TaskStepsPanel({ taskId }: TaskStepsPanelProps): ReactElement {
  const { t } = useTranslation('common');
  const propose = useProposeSteps();
  const apply = useApplySteps();

  const [granularity, setGranularity] = useState<StepGranularity>('standard');
  const [steps, setSteps] = useState<StepProposalUI[]>([]);
  const [checked, setChecked] = useState<boolean[]>([]);
  const [titles, setTitles] = useState<string[]>([]);
  const [hasProposed, setHasProposed] = useState(false);

  const handlePropose = async (): Promise<void> => {
    try {
      const result = await propose.mutateAsync({ taskId, granularity });
      setSteps(result.steps);
      setChecked(result.steps.map(() => true));
      setTitles(result.steps.map((s) => s.title));
      setHasProposed(true);
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'tasks.steps.propose_error'),
      });
    }
  };

  const handleToggle = (index: number): void => {
    setChecked((prev) => prev.map((v, i) => (i === index ? !v : v)));
  };

  const handleTitleChange = (index: number, value: string): void => {
    setTitles((prev) => prev.map((v, i) => (i === index ? value : v)));
  };

  const handleApply = async (): Promise<void> => {
    const selected = steps
      .map((step, i) => ({ step, i }))
      .filter(({ i }) => checked[i])
      .map(({ step, i }) => ({
        title: titles[i] ?? step.title,
        description: step.description,
        priority: PRIORITY_MAP[step.priority] ?? 2,
      }));

    if (selected.length === 0) return;

    try {
      const result = await apply.mutateAsync({ taskId, steps: selected });
      toaster.show({
        tone: 'success',
        message: t('tasks.steps.apply_success', { count: result.created.length }),
      });
      setSteps([]);
      setChecked([]);
      setTitles([]);
      setHasProposed(false);
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'tasks.steps.apply_error') });
    }
  };

  const selectedCount = checked.filter(Boolean).length;

  return (
    <Card
      style={{
        padding: 'var(--nf-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--nf-space-3)',
      }}
    >
      <h2 style={{ margin: 0, fontSize: 'var(--nf-text-base)' }}>{t('tasks.steps.title')}</h2>

      {!hasProposed ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
          <div>
            <span
              style={{
                display: 'block',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-muted)',
                marginBlockEnd: 'var(--nf-space-1)',
              }}
            >
              {t('tasks.steps.granularity_label')}
            </span>
            <div style={{ display: 'inline-flex', gap: 'var(--nf-space-1)' }}>
              {(['coarse', 'standard', 'fine'] as const).map((g) => (
                <button
                  key={g}
                  type="button"
                  onClick={() => {
                    setGranularity(g);
                  }}
                  style={{
                    background: granularity === g ? 'var(--nf-color-accent)' : 'transparent',
                    color:
                      granularity === g ? 'var(--nf-color-fg-on-accent)' : 'var(--nf-color-fg)',
                    border: '1px solid var(--nf-color-border)',
                    padding: 'var(--nf-space-2) var(--nf-space-3)',
                    cursor: 'pointer',
                    fontSize: 'var(--nf-text-xs)',
                    borderRadius: 'var(--nf-radius-xs)',
                  }}
                >
                  {t(`tasks.steps.granularity_${g}`)}
                </button>
              ))}
            </div>
          </div>
          {propose.isPending ? (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                padding: 'var(--nf-space-4)',
              }}
            >
              <Spinner label={t('tasks.steps.proposing')} />
            </div>
          ) : (
            <Button
              type="button"
              onClick={() => {
                void handlePropose();
              }}
            >
              {t('tasks.steps.propose_button')}
            </Button>
          )}
        </div>
      ) : (
        <>
          {steps.length === 0 ? (
            <p
              style={{
                margin: 0,
                color: 'var(--nf-color-fg-muted)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              {t('tasks.steps.empty')}
            </p>
          ) : (
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
              {steps.map((step, i) => (
                <StepItem
                  key={step.uiId}
                  step={step}
                  index={i}
                  checked={checked[i] ?? false}
                  title={titles[i] ?? step.title}
                  onToggle={handleToggle}
                  onTitleChange={handleTitleChange}
                />
              ))}
            </ul>
          )}

          <div
            style={{
              display: 'flex',
              gap: 'var(--nf-space-2)',
              justifyContent: 'flex-end',
            }}
          >
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setSteps([]);
                setChecked([]);
                setTitles([]);
                setHasProposed(false);
              }}
            >
              {t('tasks.steps.discard')}
            </Button>
            <Button
              type="button"
              disabled={selectedCount === 0 || apply.isPending}
              onClick={() => {
                void handleApply();
              }}
            >
              {apply.isPending ? t('tasks.steps.applying') : t('tasks.steps.apply_button')}
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}
