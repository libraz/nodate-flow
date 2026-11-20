/**
 * InboxItemRow — single inbox row with source icon, kind label, optional
 * linked task, relative receivedAt, and Archive / Snooze actions.
 */

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

type SourceKey = 'github' | 'slack' | 'email' | 'manual' | 'webhook';

const SOURCE_LABEL_KEY: Record<SourceKey, string> = {
  github: 'source.github',
  slack: 'source.slack',
  email: 'source.email',
  manual: 'source.manual',
  webhook: 'source.webhook',
};

/** Item kinds with a translated label; any other kind is shown as sent. */
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
  const { t, i18n } = useTranslation('inbox');
  const [sourceDialogOpen, setSourceDialogOpen] = useState(false);
  const locale = i18n.resolvedLanguage ?? 'en';
  const icon = SOURCE_ICON[item.source] ?? InboxIcon;
  const sourceLabel = isKnownSource(item.source) ? t(SOURCE_LABEL_KEY[item.source]) : item.source;
  const kindKey = KIND_LABEL_KEY[item.kind];
  const kindLabel = kindKey ? t(kindKey) : item.kind;

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
          <div className={styles.meta}>
            <Badge tone="neutral">{sourceLabel}</Badge>
            <span className={kindKey ? styles.kindLabel : styles.kind}>{kindLabel}</span>
          </div>
          {item.taskId ? (
            <Link
              to="/tasks/$taskId"
              params={{ taskId: item.taskId }}
              className={cx(styles.taskLink, 'nf-focus-ring')}
            >
              {item.taskTitle ?? item.taskId}
            </Link>
          ) : item.sourceDetail?.title ? (
            <span
              title={item.sourceDetail.title}
              style={{
                maxInlineSize: '100%',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                fontWeight: 'var(--nf-weight-medium)',
              }}
            >
              {item.sourceDetail.title}
            </span>
          ) : null}
          <span className={styles.time}>{formatRelative(item.receivedAt, locale)}</span>
        </div>
        <div className={styles.actions}>
          {item.sourceDetail?.title && !item.taskId ? (
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
