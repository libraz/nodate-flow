/**
 * InboxItemRow — single inbox row with source icon, kind label, optional
 * linked task, relative receivedAt, and Archive / Snooze actions.
 */

import Icon from '@nodate-flow/ui/icon';
import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import { Link } from '@tanstack/react-router';
import {
  Github,
  Inbox as InboxIcon,
  type LucideIcon,
  Mail,
  MessageSquare,
  Pencil,
  Webhook,
} from 'lucide-react';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import type { InboxItem } from './api';
import SnoozePopover from './snooze-popover';

export interface InboxItemRowProps {
  item: InboxItem;
  onArchive: (id: string) => void;
  onSnooze: (id: string, snoozeUntil: number) => void;
}

const SOURCE_ICON: Record<string, LucideIcon> = {
  github: Github,
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

function isKnownSource(source: string): source is SourceKey {
  return source in SOURCE_LABEL_KEY;
}

function formatRelative(unixSec: number, locale: string): string {
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  const rawDiff = unixSec - Math.floor(Date.now() / 1000);
  const diffSec = rawDiff > 0 ? 0 : rawDiff;
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
  const locale = i18n.resolvedLanguage ?? 'en';
  const icon = SOURCE_ICON[item.source] ?? InboxIcon;
  const sourceLabel = isKnownSource(item.source) ? t(SOURCE_LABEL_KEY[item.source]) : item.source;

  return (
    <Card style={{ padding: '0.875rem 1rem' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '0.875rem',
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            inlineSize: '2rem',
            blockSize: '2rem',
            borderRadius: '0.5rem',
            background: 'var(--nf-color-surface)',
            color: 'var(--nf-color-fg)',
            flexShrink: 0,
          }}
        >
          <Icon icon={icon} decorative />
        </div>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '0.25rem',
            minInlineSize: 0,
            flex: 1,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
            <Badge tone="neutral">{sourceLabel}</Badge>
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: '0.8125rem',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {item.kind}
            </span>
          </div>
          {item.taskId ? (
            <Link
              to="/tasks/$taskId"
              params={{ taskId: item.taskId }}
              style={{
                color: 'var(--nf-color-fg)',
                textDecoration: 'none',
                fontWeight: 500,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {item.taskTitle ?? item.taskId}
            </Link>
          ) : null}
          <span
            style={{
              color: 'var(--nf-color-fg-muted)',
              fontSize: '0.8125rem',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {formatRelative(item.receivedAt, locale)}
          </span>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem', flexShrink: 0 }}>
          <SnoozePopover
            onSnooze={(snoozeUntil) => {
              onSnooze(item.id, snoozeUntil);
            }}
          >
            <Button type="button" variant="ghost" size="sm">
              {t('action.snooze')}
            </Button>
          </SnoozePopover>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              onArchive(item.id);
            }}
          >
            {t('action.archive')}
          </Button>
        </div>
      </div>
    </Card>
  );
}
