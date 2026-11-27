import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import type { Zone } from '@nodate-flow/ui/time';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { formatEpoch } from '../../../lib/format';
import type { ActionErrorSlot } from './action-error';
import type { DecideHandoffArgs, WorkflowHandoff } from './api';
import { HANDOFF_ROLE_KEY, isHandoffRole } from './handoff-roles';
import css from './task-workflow-panel.module.css';

type HandoffStatus = 'pending' | 'accepted' | 'declined' | 'expired' | 'cancelled';

const HANDOFF_STATUS_KEY: Record<HandoffStatus, string> = {
  pending: 'taskWorkflow.handoffStatus.pending',
  accepted: 'taskWorkflow.handoffStatus.accepted',
  declined: 'taskWorkflow.handoffStatus.declined',
  expired: 'taskWorkflow.handoffStatus.expired',
  cancelled: 'taskWorkflow.handoffStatus.cancelled',
};

const HANDOFF_STATUS_TONE: Record<HandoffStatus, BadgeTone> = {
  pending: 'info',
  accepted: 'success',
  declined: 'neutral',
  expired: 'neutral',
  cancelled: 'neutral',
};

function isHandoffStatus(value: string): value is HandoffStatus {
  return value in HANDOFF_STATUS_KEY;
}

interface HandoffHistoryProps {
  handoffs: WorkflowHandoff[];
  memberName: (userId: string | undefined) => string | undefined;
  locale: string;
  zone: Zone;
  actionErrors: ActionErrorSlot;
  pendingHandoffDecision: DecideHandoffArgs | undefined;
  onDecide: (handoff: WorkflowHandoff, decision: 'accept' | 'decline' | 'cancel') => Promise<void>;
}

/** Lists handoff requests with their status and the accept, decline and withdraw controls. */
export default function HandoffHistory({
  handoffs,
  memberName,
  locale,
  zone,
  actionErrors,
  pendingHandoffDecision,
  onDecide,
}: HandoffHistoryProps): ReactElement {
  const { t } = useTranslation('common');
  const { errorFor } = actionErrors;
  const handoffRoleLabel = (role: string): string =>
    isHandoffRole(role) ? t(HANDOFF_ROLE_KEY[role]) : role;
  return (
    <section className={css.section} aria-label={t('taskWorkflow.handoffHistory')}>
      <h3 className={css.sectionTitle}>{t('taskWorkflow.handoffHistory')}</h3>
      <ul className={`${css.list} ${css.dividedList}`}>
        {handoffs.map((handoff) => {
          const status = isHandoffStatus(handoff.status) ? handoff.status : undefined;
          const deciding =
            pendingHandoffDecision?.handoffId === handoff.id
              ? pendingHandoffDecision.input.decision
              : undefined;
          const canWithdraw = status === 'pending' && handoff.canCancel;
          return (
            <li key={handoff.id} className={css.handoffItem}>
              <div className={css.itemBody}>
                <div className={css.itemHeader}>
                  <strong>
                    {t('taskWorkflow.handoffSummary', {
                      role: handoffRoleLabel(handoff.role),
                      recipient: memberName(handoff.toUserId) ?? t('taskWorkflow.unknownMember'),
                    })}
                  </strong>
                  <Badge tone={status ? HANDOFF_STATUS_TONE[status] : 'neutral'}>
                    {status ? t(HANDOFF_STATUS_KEY[status]) : handoff.status}
                  </Badge>
                  {handoff.dueAt ? (
                    <span className={`${css.muted} ${css.numeric}`}>
                      {t('taskWorkflow.handoffDueSuffix', {
                        due: formatEpoch(handoff.dueAt, locale, zone) ?? '',
                      })}
                    </span>
                  ) : null}
                </div>
                {status === 'pending' ? (
                  <p className={css.muted}>{t('taskWorkflow.handoffPendingHint')}</p>
                ) : status === 'expired' ? (
                  <p className={css.muted}>{t('taskWorkflow.handoffExpiredHint')}</p>
                ) : null}
              </div>
              {status === 'pending' && (handoff.canDecide || canWithdraw) ? (
                <div className={css.row}>
                  {canWithdraw ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="danger"
                      disabled={pendingHandoffDecision !== undefined}
                      aria-busy={deciding === 'cancel' || undefined}
                      onClick={() => void onDecide(handoff, 'cancel')}
                    >
                      {deciding === 'cancel'
                        ? t('taskWorkflow.withdrawingHandoff')
                        : t('taskWorkflow.withdrawHandoff')}
                    </Button>
                  ) : null}
                  {handoff.canDecide ? (
                    <>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={pendingHandoffDecision !== undefined}
                        aria-busy={deciding === 'decline' || undefined}
                        onClick={() => void onDecide(handoff, 'decline')}
                      >
                        {deciding === 'decline'
                          ? t('taskWorkflow.saving')
                          : t('taskWorkflow.declineHandoff')}
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="primary"
                        disabled={pendingHandoffDecision !== undefined}
                        aria-busy={deciding === 'accept' || undefined}
                        onClick={() => void onDecide(handoff, 'accept')}
                      >
                        {deciding === 'accept'
                          ? t('taskWorkflow.saving')
                          : t('taskWorkflow.acceptHandoff')}
                      </Button>
                    </>
                  ) : null}
                </div>
              ) : null}
              {errorFor('handoffDecision', handoff.id)}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
