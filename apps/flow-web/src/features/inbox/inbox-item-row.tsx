/**
 * InboxItemRow — single inbox row with source icon, kind label, optional
 * linked task, relative receivedAt, and Archive / Snooze actions.
 */

import { lookupSignalKind } from '@nodate-flow/sdk';
import Icon from '@nodate-flow/ui/icon';
import { cx } from '@nodate-flow/ui/lib/cx';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Link } from '@tanstack/react-router';
import {
  GitPullRequest,
  Inbox as InboxIcon,
  type LucideIcon,
  Mail,
  MessageSquare,
  Pencil,
  Webhook,
} from 'lucide-react';
import { type ButtonHTMLAttributes, type ReactElement, type Ref, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatEpochDateTime } from '../../lib/format';
import { useEffectiveZone } from '../../lib/use-effective-timezone';
import type { InboxItem } from './api';
import styles from './inbox.module.css';
import SnoozePopover from './snooze-popover';
import SourceIntakeDialog from './source-intake/source-intake-dialog';

export interface InboxItemRowProps {
  item: InboxItem;
  onArchive: (id: string) => void;
  onSnooze: (id: string, snoozeUntil: number) => void;
}

const SOURCE_ICON: Record<string, LucideIcon> = {
  github: GitPullRequest,
  slack: MessageSquare,
  email: Mail,
  manual: Pencil,
  webhook: Webhook,
};

type SourceKey = 'discord' | 'github' | 'slack' | 'email' | 'manual' | 'webhook';

const SOURCE_LABEL_KEY: Record<SourceKey, string> = {
  discord: 'source.discord',
  github: 'source.github',
  slack: 'source.slack',
  email: 'source.email',
  manual: 'source.manual',
  webhook: 'source.webhook',
};

/** Item kinds outside the signal-kind registry that carry their own label. */
const KIND_LABEL_KEY: Record<string, string> = {
  'agent.task.handoff_to_user': 'kind.agent_task_handoff_to_user',
};

type SnoozeTriggerProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  ref?: Ref<HTMLElement>;
  hint: string;
  label: string;
};

/**
 * The popover clones its child with a ref and interaction props; the tooltip
 * needs the button's own ref, so the popover anchors on a span and its
 * interaction props go to the button.
 */
function SnoozeTrigger({ ref, hint, label, ...buttonProps }: SnoozeTriggerProps): ReactElement {
  return (
    <span ref={ref} className={styles.popoverAnchor}>
      <Tooltip content={hint}>
        <Button type="button" variant="ghost" size="sm" {...buttonProps}>
          {label}
        </Button>
      </Tooltip>
    </span>
  );
}

function isKnownSource(source: string): source is SourceKey {
  return source in SOURCE_LABEL_KEY;
}

function formatRelative(unixSec: number, locale: string): string {
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  const rawDiff = unixSec - Math.floor(Date.now() / 1000);
  const diffSec = Math.min(rawDiff, 0);
  const abs = Math.abs(diffSec);
  if (abs < 60) return rtf.format(Math.round(diffSec), 'second');
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), 'minute');
  if (abs < 86_400) return rtf.format(Math.round(diffSec / 3600), 'hour');
  if (abs < 2_592_000) return rtf.format(Math.round(diffSec / 86_400), 'day');
  return rtf.format(Math.round(diffSec / 2_592_000), 'month');
}

export default function InboxItemRow({
  item,
  onArchive,
  onSnooze,
}: InboxItemRowProps): ReactElement {
  const { t, i18n } = useTranslation(['inbox', 'signal-kinds']);
  const [sourceDialogOpen, setSourceDialogOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';
  const icon = SOURCE_ICON[item.source] ?? InboxIcon;
  const sourceLabel = isKnownSource(item.source) ? t(SOURCE_LABEL_KEY[item.source]) : item.source;
  const kindKey = KIND_LABEL_KEY[item.kind];
  const signalKind = lookupSignalKind(item.kind);
  // A kind neither table names is shown as sent.
  let kindLabel = item.kind;
  if (kindKey) kindLabel = t(kindKey);
  else if (signalKind) {
    kindLabel = t(`signal-kinds:${signalKind.i18nKey}.label`, { keySeparator: false });
  }
  const kindTranslated = kindLabel !== item.kind;
  const zone = useEffectiveZone();
  const absoluteTime = formatEpochDateTime(item.receivedAt, locale, zone);
  // The kind stands in as the headline when the row has no task or source title.
  // The API falls back to the kind when a source has no title of its own.
  const sourceTitle = item.sourceDetail?.title === item.kind ? undefined : item.sourceDetail?.title;
  const itemTitle = item.taskId ? item.taskTitle : sourceTitle;
  const hasTitle = Boolean(itemTitle);
  // A source with nothing of its own to read has nothing to review.
  const canReview =
    !item.taskId && Boolean(sourceTitle || item.sourceDetail?.body || item.sourceDetail?.sourceUrl);
  const headline = itemTitle || kindLabel;

  const handleArchive = (): void => {
    onArchive(item.id);
  };

  const handleSnooze = (snoozeUntil: number): void => {
    onSnooze(item.id, snoozeUntil);
  };

  return (
    <Card className={styles.row}>
      <div className={styles.rowInner}>
        <div className={styles.sourceIcon}>
          <Icon icon={icon} decorative />
        </div>
        <div className={styles.content}>
          {item.taskId ? (
            <Link
              to="/tasks/$taskId"
              params={{ taskId: item.taskId }}
              title={headline}
              className={cx(styles.taskLink, 'nf-focus-ring')}
            >
              {headline}
            </Link>
          ) : (
            <span className={styles.headline} title={headline}>
              {headline}
            </span>
          )}
          <div className={styles.meta}>
            <Badge tone="neutral">{sourceLabel}</Badge>
            {hasTitle ? (
              <span className={kindTranslated ? styles.kindLabel : styles.kind}>{kindLabel}</span>
            ) : null}
          </div>
          <span className={styles.time} title={absoluteTime ?? undefined}>
            {formatRelative(item.receivedAt, locale)}
          </span>
        </div>
        <div className={styles.actions}>
          {canReview ? (
            <Tooltip content={t('sourceIntake.review_hint')}>
              <Button type="button" size="sm" onClick={() => setSourceDialogOpen(true)}>
                {t('sourceIntake.review')}
              </Button>
            </Tooltip>
          ) : null}
          <SnoozePopover onSnooze={handleSnooze}>
            <SnoozeTrigger hint={t('action.snooze_hint')} label={t('action.snooze')} />
          </SnoozePopover>
          <Tooltip content={t('action.archive_hint')}>
            <Button type="button" variant="ghost" size="sm" onClick={handleArchive}>
              {t('action.archive')}
            </Button>
          </Tooltip>
        </div>
      </div>
      {sourceDialogOpen ? (
        <Suspense fallback={null}>
          <SourceIntakeDialog
            workspaceId={item.workspaceId}
            item={item}
            open={sourceDialogOpen}
            onClose={() => setSourceDialogOpen(false)}
          />
        </Suspense>
      ) : null}
    </Card>
  );
}
