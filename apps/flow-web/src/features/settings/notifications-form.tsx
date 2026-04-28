/**
 * NotificationsForm — edit the authenticated user's notification channel
 * toggles. Parent must wrap this in `<Suspense>` because it consumes
 * `useMeQuery` (Suspense mode).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useSubmitGuard } from '../../lib/use-submit-guard';
import { type PatchMeInput, useMeQuery, useUpdateMe } from './api';
import styles from './notifications-form.module.css';

interface ToggleKey {
  readonly key:
    | 'notifEmailDigest'
    | 'notifEmailMention'
    | 'notifEmailAssignment'
    | 'notifEmailDueSoon'
    | 'notifWebPush';
  readonly labelKey: string;
  readonly descriptionKey: string;
}

const TOGGLES: readonly ToggleKey[] = [
  {
    key: 'notifEmailDigest',
    labelKey: 'notifications.email_digest.label',
    descriptionKey: 'notifications.email_digest.description',
  },
  {
    key: 'notifEmailMention',
    labelKey: 'notifications.email_mention.label',
    descriptionKey: 'notifications.email_mention.description',
  },
  {
    key: 'notifEmailAssignment',
    labelKey: 'notifications.email_assignment.label',
    descriptionKey: 'notifications.email_assignment.description',
  },
  {
    key: 'notifEmailDueSoon',
    labelKey: 'notifications.email_due_soon.label',
    descriptionKey: 'notifications.email_due_soon.description',
  },
  {
    key: 'notifWebPush',
    labelKey: 'notifications.web_push.label',
    descriptionKey: 'notifications.web_push.description',
  },
] as const;

type ToggleState = Record<ToggleKey['key'], boolean>;

export default function NotificationsForm(): ReactElement {
  const { t } = useTranslation('settings');
  const { data: me } = useMeQuery();
  const update = useUpdateMe();

  const [state, setState] = useState<ToggleState>({
    notifEmailDigest: me.notifEmailDigest,
    notifEmailMention: me.notifEmailMention,
    notifEmailAssignment: me.notifEmailAssignment,
    notifEmailDueSoon: me.notifEmailDueSoon,
    notifWebPush: me.notifWebPush,
  });
  const submitGuard = useSubmitGuard();

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (submitGuard.guard()) return;
    const patch: PatchMeInput = { ...state };
    try {
      await update.mutateAsync(patch);
      toaster.show({ tone: 'success', message: t('notifications.saved') });
    } catch {
      toaster.show({ tone: 'danger', message: t('notifications.errors.update_failed') });
    } finally {
      submitGuard.end();
    }
  };

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      className={styles.form}
    >
      <ul className={styles.list}>
        {TOGGLES.map((item) => {
          const checked = state[item.key];
          const id = `notif-${item.key}`;
          return (
            <li key={item.key} className={styles.row}>
              <div className={styles.identity}>
                <label htmlFor={id} className={styles.label}>
                  {t(item.labelKey)}
                </label>
                <span className={styles.helpText}>{t(item.descriptionKey)}</span>
              </div>
              <Switch
                id={id}
                checked={checked}
                onCheckedChange={(next) => {
                  setState((prev) => ({ ...prev, [item.key]: next }));
                }}
                aria-label={t(item.labelKey)}
              />
            </li>
          );
        })}
      </ul>

      <div className={styles.actions}>
        <Button type="submit" variant="primary" disabled={submitGuard.submitting}>
          {submitGuard.submitting ? t('notifications.saving') : t('notifications.save')}
        </Button>
      </div>
    </form>
  );
}
