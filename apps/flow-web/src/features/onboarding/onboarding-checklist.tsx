/**
 * OnboardingChecklist — the "getting started" card on the home page.
 * Shown to people with no workspace yet and to admins/owners of the
 * current workspace; completion is derived, only dismissal is stored.
 */

import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Button, { buttonClassName } from '@nodate-flow/ui/primitives/button';
import { Link } from '@tanstack/react-router';
import { Check } from 'lucide-react';
import { type ReactElement, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { OPEN_CREATE_TASK_EVENT } from '../../components/layout/glass-dock';
import { selectUser, useAuth } from '../auth/auth-store';
import ProjectCreateDialog from '../projects/project-create-dialog';
import styles from './onboarding-checklist.module.css';
import { type OnboardingStepId, useOnboardingSteps } from './use-onboarding-steps';

const DISMISS_KEY_PREFIX = 'nf.onboarding.dismissed.';

function readDismissed(userId: string | undefined): boolean {
  if (!userId) return false;
  try {
    return window.localStorage.getItem(`${DISMISS_KEY_PREFIX}${userId}`) === '1';
  } catch {
    return false;
  }
}

export default function OnboardingChecklist(): ReactElement | null {
  const { t } = useTranslation('common');
  const user = useAuth(selectUser);
  const { eligible, workspaceId, steps, doneCount } = useOnboardingSteps();
  const [dismissed, setDismissed] = useState(() => readDismissed(user?.id));
  const [projectDialogOpen, setProjectDialogOpen] = useState(false);
  const titleId = useId();
  const labelId = useId();

  if (!eligible || dismissed || doneCount === steps.length) return null;

  const handleDismiss = (): void => {
    if (user?.id) {
      try {
        window.localStorage.setItem(`${DISMISS_KEY_PREFIX}${user.id}`, '1');
      } catch {
        /* storage unavailable: hide for this session only */
      }
    }
    setDismissed(true);
  };

  const stepTitle = (id: OnboardingStepId): string => {
    switch (id) {
      case 'workspace':
        return t('onboarding.steps.workspace.title');
      case 'project':
        return t('onboarding.steps.project.title');
      case 'task':
        return t('onboarding.steps.task.title');
      case 'invite':
        return t('onboarding.steps.invite.title');
    }
  };
  const stepBody = (id: OnboardingStepId): string => {
    switch (id) {
      case 'workspace':
        return t('onboarding.steps.workspace.body');
      case 'project':
        return t('onboarding.steps.project.body');
      case 'task':
        return t('onboarding.steps.task.body');
      case 'invite':
        return t('onboarding.steps.invite.body');
    }
  };

  const renderAction = (id: OnboardingStepId, primary: boolean): ReactElement | null => {
    const label = stepTitle(id);
    const linkClass = primary
      ? buttonClassName({ variant: 'primary' })
      : cx(styles.textLink, 'nf-focus-ring');
    const renderButton = (onClick: () => void): ReactElement =>
      primary ? (
        <Button type="button" variant="primary" onClick={onClick}>
          {label}
        </Button>
      ) : (
        <button type="button" className={cx(styles.textLink, 'nf-focus-ring')} onClick={onClick}>
          {label}
        </button>
      );
    if (id === 'workspace') {
      return (
        <Link to="/setup" className={linkClass}>
          {label}
        </Link>
      );
    }
    // The remaining steps live inside a workspace.
    if (!workspaceId) return null;
    switch (id) {
      case 'project':
        return renderButton(() => setProjectDialogOpen(true));
      case 'task':
        return renderButton(() => window.dispatchEvent(new Event(OPEN_CREATE_TASK_EVENT)));
      case 'invite':
        return (
          <Link to="/workspaces/$id/members" params={{ id: workspaceId }} className={linkClass}>
            {label}
          </Link>
        );
    }
  };

  const nextId = steps.find((s) => !s.done)?.id;

  return (
    <section aria-labelledby={titleId} className={styles.card}>
      <div className={styles.head}>
        <h2 id={titleId} className={styles.title}>
          {t('onboarding.title')}
        </h2>
        <button
          type="button"
          className={cx(styles.dismiss, 'nf-focus-ring')}
          onClick={handleDismiss}
        >
          {t('onboarding.dismiss')}
        </button>
      </div>

      <div className={styles.progress}>
        <span id={labelId} className={styles.progressLabel}>
          {t('onboarding.progress', { done: doneCount, total: steps.length })}
        </span>
        <div
          role="progressbar"
          aria-labelledby={labelId}
          aria-valuemin={0}
          aria-valuemax={steps.length}
          aria-valuenow={doneCount}
          className={styles.bar}
        >
          <div
            className={styles.barFill}
            style={{ inlineSize: `${(doneCount / steps.length) * 100}%` }}
          />
        </div>
      </div>

      <ol className={styles.steps}>
        {steps.map((step, index) => {
          const isNext = step.id === nextId;
          return (
            <li
              key={step.id}
              className={cx(styles.step, isNext && styles.stepNext, step.done && styles.stepDone)}
            >
              <span
                aria-hidden
                className={cx(
                  styles.badge,
                  isNext && styles.badgeNext,
                  step.done && styles.badgeDone,
                )}
              >
                {step.done ? <Icon icon={Check} decorative /> : index + 1}
              </span>
              <div className={styles.text}>
                <span className={styles.stepTitle}>{stepTitle(step.id)}</span>
                {step.done ? (
                  <span className={styles.visuallyHidden}>{t('onboarding.steps.done')}</span>
                ) : (
                  <span className={styles.stepBody}>{stepBody(step.id)}</span>
                )}
              </div>
              {step.done ? null : renderAction(step.id, isNext)}
            </li>
          );
        })}
      </ol>

      {workspaceId ? (
        <ProjectCreateDialog
          workspaceId={workspaceId}
          open={projectDialogOpen}
          onClose={() => setProjectDialogOpen(false)}
        />
      ) : null}
    </section>
  );
}
