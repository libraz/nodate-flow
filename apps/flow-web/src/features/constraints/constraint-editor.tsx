/**
 * ConstraintEditor — minimal constraint editor panel.
 *
 * Lets the user author a JSON-DSL expression, save it as a new
 * task_constraints row, and run the engine on demand to see which
 * constraints are currently satisfied. Designed to slot into the task
 * detail drawer.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { AlertTriangle, CheckCircle2, XCircle } from 'lucide-react';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';
import PageHelp from '../../components/page-help';
import SubmitHint from '../../components/submit-hint';
import { confirmAction } from '../../lib/confirm-action';
import { onSubmitShortcut } from '../../lib/submit-shortcut';
import { AiSparkle } from '../ai-suggestions/ai-consequence';

import {
  type ConstraintKind,
  type ConstraintOutcome,
  useAddConstraint,
  useCompileConstraint,
  useEvaluateConstraints,
  useExplainConstraint,
  useRemoveConstraint,
} from './api';
import styles from './constraints.module.css';

const KINDS: ConstraintKind[] = ['deadline', 'dependency', 'approval', 'signal', 'custom'];

export const KIND_LABEL_KEY: Record<ConstraintKind, string> = {
  deadline: 'editor.kinds.deadline',
  dependency: 'editor.kinds.dependency',
  approval: 'editor.kinds.approval',
  signal: 'editor.kinds.signal',
  custom: 'editor.kinds.custom',
};

export interface ConstraintEditorProps {
  taskId: string;
}

function isKind(v: string): v is ConstraintKind {
  return (KINDS as readonly string[]).includes(v);
}

export default function ConstraintEditor({ taskId }: ConstraintEditorProps): ReactElement {
  const { t } = useTranslation('constraints');
  const [kind, setKind] = useState<ConstraintKind>('custom');
  const [expression, setExpression] = useState('');
  const [nlPrompt, setNlPrompt] = useState('');
  const [explanation, setExplanation] = useState<string | null>(null);
  const [outcomes, setOutcomes] = useState<ConstraintOutcome[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);

  const addMut = useAddConstraint();
  const evalMut = useEvaluateConstraints();
  const compileMut = useCompileConstraint();
  const explainMut = useExplainConstraint();
  const removeMut = useRemoveConstraint();

  // The row cannot be re-created from an outcome, so removal asks first.
  const onRemove = async (constraintId: string): Promise<void> => {
    setError(null);
    const confirmed = await confirmAction({
      title: t('editor.removeConfirmTitle'),
      message: t('editor.removeConfirm'),
      confirmLabel: t('editor.remove'),
      tone: 'danger',
    });
    if (!confirmed) return;
    setRemovingId(constraintId);
    removeMut.mutate(
      { taskId, constraintId },
      {
        onError: () => setError(t('editor.errors.removeFailed')),
        onSuccess: () =>
          setOutcomes((prev) => (prev ? prev.filter((o) => o.id !== constraintId) : prev)),
        onSettled: () => setRemovingId(null),
      },
    );
  };

  const onSubmit = (ev: FormEvent<HTMLFormElement>): void => {
    ev.preventDefault();
    setError(null);
    addMut.mutate(
      { taskId, kind, expression },
      {
        onError: () => setError(t('editor.errors.addFailed')),
        onSuccess: () => setExpression(''),
      },
    );
  };

  const onEvaluate = (): void => {
    setError(null);
    evalMut.mutate(
      { taskId },
      {
        onSuccess: (r) => setOutcomes(r.outcomes),
        onError: () => setError(t('editor.errors.evaluateFailed')),
      },
    );
  };

  const handleCompile = (): void => {
    setError(null);
    setExplanation(null);
    compileMut.mutate(
      { taskId, prompt: nlPrompt },
      {
        onSuccess: (r) => {
          const v = r.kind;
          if (isKind(v)) setKind(v);
          setExpression(r.expression);
          setNlPrompt('');
        },
        onError: () => setError(t('editor.errors.compileFailed')),
      },
    );
  };

  const handleExplain = (): void => {
    setError(null);
    setExplanation(null);
    explainMut.mutate(
      { taskId, expression },
      {
        onSuccess: (r) => setExplanation(r.explanation),
        onError: () => setError(t('editor.errors.explainFailed')),
      },
    );
  };

  return (
    <section aria-label={t('editor.title')} className={styles.editor}>
      <div className={styles.editorHead}>
        <h3 className={styles.editorTitle}>{t('editor.title')}</h3>
        <PageHelp topic="task-constraints" />
      </div>
      <div className={styles.nlRow}>
        <FormField label={t('editor.nlPrompt')} description={t('editor.nlHint')}>
          {(control) => (
            <Input
              {...control}
              value={nlPrompt}
              placeholder={t('editor.nlPrompt')}
              onChange={(e) => setNlPrompt(e.currentTarget.value)}
            />
          )}
        </FormField>
        <Button
          type="button"
          onClick={handleCompile}
          disabled={compileMut.isPending || !nlPrompt.trim()}
          aria-busy={compileMut.isPending || undefined}
          className={styles.nlGenerate}
        >
          <AiSparkle kind="suggests" />
          {t('editor.generate')}
        </Button>
      </div>

      <form onSubmit={onSubmit} onKeyDown={onSubmitShortcut}>
        <details className={styles.advanced} open={expression.length > 0}>
          <summary className={styles.advancedSummary}>{t('editor.advanced')}</summary>
          <FormField label={t('editor.kind')}>
            {(control) => (
              <Select
                {...control}
                value={kind}
                onChange={(e) => {
                  const v = e.currentTarget.value;
                  if (isKind(v)) setKind(v);
                }}
              >
                {KINDS.map((k) => (
                  <option key={k} value={k}>
                    {t(KIND_LABEL_KEY[k])}
                  </option>
                ))}
              </Select>
            )}
          </FormField>
          <FormField label={t('editor.expression')}>
            {(control) => (
              <Textarea
                {...control}
                value={expression}
                placeholder={t('editor.placeholder')}
                onChange={(e) => setExpression(e.currentTarget.value)}
                rows={4}
                spellCheck={false}
              />
            )}
          </FormField>
        </details>
        <div className={styles.editorActions}>
          <SubmitHint />
          <Button type="submit" disabled={addMut.isPending || !expression.trim()}>
            {t('editor.add')}
          </Button>
          <Button
            type="button"
            onClick={onEvaluate}
            disabled={evalMut.isPending}
            aria-busy={evalMut.isPending || undefined}
          >
            {t('editor.evaluate')}
          </Button>
          <Button
            type="button"
            onClick={handleExplain}
            disabled={explainMut.isPending || !expression.trim()}
            aria-busy={explainMut.isPending || undefined}
          >
            <AiSparkle kind="suggests" />
            {t('editor.explain')}
          </Button>
        </div>
      </form>

      {explanation ? (
        <div>
          <h4 className={styles.subheading}>{t('editor.explanation')}</h4>
          <p className={styles.explanation}>{explanation}</p>
        </div>
      ) : null}

      {error ? <p role="alert">{error}</p> : null}

      {outcomes ? (
        <div role="group" aria-label={t('editor.outcomes')}>
          <h4 className={styles.subheading}>{t('editor.outcomes')}</h4>
          <ul className={styles.outcomes}>
            {outcomes.map((o) => (
              <li key={o.id} className={styles.outcome}>
                <span className={styles.outcomeStatus} data-status={outcomeStatus(o)}>
                  {o.parseError ? (
                    <AlertTriangle size={14} aria-hidden="true" />
                  ) : o.satisfied ? (
                    <CheckCircle2 size={14} aria-hidden="true" />
                  ) : (
                    <XCircle size={14} aria-hidden="true" />
                  )}
                  {o.parseError
                    ? t('editor.parseError')
                    : o.satisfied
                      ? t('editor.satisfied')
                      : t('editor.failed')}
                </span>
                <span className={styles.outcomeBody}>
                  <span className={styles.outcomeKind}>{t(KIND_LABEL_KEY[o.kind])}</span>
                  <code className={styles.outcomeExpr} title={o.expression}>
                    {o.expression}
                  </code>
                  {o.parseError ? (
                    <span className={styles.outcomeUnknown}>{o.parseError}</span>
                  ) : null}
                </span>
                <Button
                  type="button"
                  variant="danger"
                  size="sm"
                  onClick={() => void onRemove(o.id)}
                  disabled={removeMut.isPending}
                  aria-busy={removingId === o.id || undefined}
                >
                  {t('editor.remove')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className={styles.evaluateHint}>{t('editor.evaluateHint')}</p>
      )}
    </section>
  );
}

function outcomeStatus(o: ConstraintOutcome): 'parse' | 'satisfied' | 'failed' {
  if (o.parseError) return 'parse';
  return o.satisfied ? 'satisfied' : 'failed';
}
