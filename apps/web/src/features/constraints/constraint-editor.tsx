/**
 * ConstraintEditor — 3.WEB-1 minimal constraint editor panel.
 *
 * Lets the user author a JSON-DSL expression, save it as a new
 * task_constraints row, and run the engine on demand to see which
 * constraints are currently satisfied. Designed to slot into the task
 * detail drawer.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Select from '@nodate-flow/ui/primitives/select';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  type ConstraintKind,
  type ConstraintOutcome,
  useAddConstraint,
  useEvaluateConstraints,
  useRemoveConstraint,
} from './api';

const KINDS: ConstraintKind[] = ['deadline', 'dependency', 'approval', 'signal', 'custom'];

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
  const [outcomes, setOutcomes] = useState<ConstraintOutcome[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const addMut = useAddConstraint();
  const evalMut = useEvaluateConstraints();
  const removeMut = useRemoveConstraint();

  const onRemove = (constraintId: string): void => {
    setError(null);
    removeMut.mutate(
      { taskId, constraintId },
      {
        onError: () => setError(t('editor.errors.removeFailed')),
        onSuccess: () =>
          setOutcomes((prev) => (prev ? prev.filter((o) => o.id !== constraintId) : prev)),
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

  return (
    <section aria-label={t('editor.title')}>
      <h3>{t('editor.title')}</h3>
      <form onSubmit={onSubmit}>
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
                  {t(`editor.kinds.${k}`)}
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
        <div>
          <Button type="submit" disabled={addMut.isPending || !expression.trim()}>
            {t('editor.add')}
          </Button>
          <Button type="button" onClick={onEvaluate} disabled={evalMut.isPending}>
            {t('editor.evaluate')}
          </Button>
        </div>
      </form>

      {error ? <p role="alert">{error}</p> : null}

      {outcomes ? (
        <div aria-label={t('editor.outcomes')}>
          <h4>{t('editor.outcomes')}</h4>
          <ul>
            {outcomes.map((o) => (
              <li key={o.id}>
                <code>{o.id}</code>:{' '}
                {o.parseError
                  ? `${t('editor.parseError')}: ${o.parseError}`
                  : o.satisfied
                    ? t('editor.satisfied')
                    : t('editor.failed')}{' '}
                <Button type="button" onClick={() => onRemove(o.id)} disabled={removeMut.isPending}>
                  {t('editor.remove')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}
