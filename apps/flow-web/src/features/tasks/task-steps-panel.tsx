/**
 * TaskStepsPanel — collapsible panel for AI-powered task decomposition.
 *
 * Lets the user propose subtask steps via AI, review / edit / toggle them,
 * then apply selected steps as real subtasks.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import Input from '@nodate-flow/ui/primitives/input';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import motion from '@nodate-flow/ui/styles/list-motion.module.css';
import { type CSSProperties, type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { PRIORITY_KEY, PRIORITY_TONE } from './constants';
import {
  type StepGranularity,
  type StepProposalUI,
  useApplySteps,
  useProposeSteps,
} from './steps-api';
import css from './task-steps-panel.module.css';

const GRANULARITY_KEY: Record<StepGranularity, string> = {
  coarse: 'tasks.steps.granularity_coarse',
  standard: 'tasks.steps.granularity_standard',
  fine: 'tasks.steps.granularity_fine',
};

/** Proposed steps arrive together; items past this index enter with the last staggered one. */
const STAGGER_LIMIT = 8;

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
  const descriptionId = useId();

  return (
    <li
      data-checked={checked}
      className={cx(css.step, motion.enter)}
      style={{ '--stagger-index': Math.min(index, STAGGER_LIMIT) } as CSSProperties}
    >
      <div className={css.stepRow}>
        <Checkbox
          id={checkboxId}
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
          className={css.stepTitle}
        />
        <Badge tone={PRIORITY_TONE[step.priority] ?? 'neutral'}>
          {t(PRIORITY_KEY[step.priority] ?? 'tasks.priority.none')}
        </Badge>
      </div>
      {step.description.length > 0 ? (
        // Indented under the checkbox (space-6 + space-1).
        <div className={css.details}>
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={descriptionId}
            onClick={() => {
              setExpanded((prev) => !prev);
            }}
            className={css.toggle}
          >
            {expanded ? t('tasks.steps.collapse') : t('tasks.steps.expand')}
          </button>
          <Collapse open={expanded} id={descriptionId}>
            <p className={css.description}>{step.description}</p>
          </Collapse>
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
        // Straight through: propose-steps and apply-steps speak the same
        // scale, so there is nothing left to translate here. The panel
        // used to map a label to a number and quietly send 2 for
        // anything it had no entry for, which silently rewrote the
        // priority the user was shown.
        priority: step.priority,
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
    <Card className={css.card}>
      <h2 className={css.title}>{t('tasks.steps.title')}</h2>

      {!hasProposed ? (
        <div key="form" className={cx(css.form, css.stage)}>
          <div>
            <span className={css.fieldLabel}>{t('tasks.steps.granularity_label')}</span>
            <SegmentedControl<StepGranularity>
              ariaLabel={t('tasks.steps.granularity_label')}
              size="sm"
              value={granularity}
              disabled={propose.isPending}
              onChange={setGranularity}
              options={(['coarse', 'standard', 'fine'] as const).map((g) => ({
                value: g,
                label: t(GRANULARITY_KEY[g]),
              }))}
            />
          </div>
          {/* The button stays in place while the proposal runs, so the panel does not jump. */}
          <Button
            type="button"
            disabled={propose.isPending}
            aria-busy={propose.isPending}
            onClick={() => {
              void handlePropose();
            }}
          >
            {propose.isPending ? t('tasks.steps.proposing') : t('tasks.steps.propose_button')}
          </Button>
        </div>
      ) : (
        <div key="proposal" className={cx(css.proposal, css.stage)}>
          {steps.length === 0 ? (
            <p className={css.empty}>{t('tasks.steps.empty')}</p>
          ) : (
            <ul className={css.list}>
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

          <div className={css.actions}>
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
        </div>
      )}
    </Card>
  );
}
