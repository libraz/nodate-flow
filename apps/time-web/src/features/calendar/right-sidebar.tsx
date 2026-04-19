import { Bell, Link, NotebookPen, Users, X } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Badge from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Input from '@nodate-flow/ui/primitives/input';

import { type RightPanel, useCalendarUi } from '../../stores/calendar-ui-store';
import { useWorkspace } from '../../stores/workspace-store';
import { useCalendarsQuery, useMembersQuery } from './api';

function MemoTab(): ReactElement {
  const { t } = useTranslation();
  const [memos, setMemos] = useState<{ id: string; text: string; done: boolean }[]>([]);
  const [newMemo, setNewMemo] = useState('');

  const handleAdd = () => {
    if (!newMemo.trim()) return;
    setMemos((prev) => [...prev, { id: crypto.randomUUID(), text: newMemo.trim(), done: false }]);
    setNewMemo('');
  };

  const toggleDone = (id: string) => {
    setMemos((prev) => prev.map((m) => (m.id === id ? { ...m, done: !m.done } : m)));
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
      <div style={{ display: 'flex', gap: 'var(--nf-space-2)' }}>
        <Input
          type="text"
          value={newMemo}
          onChange={(e) => setNewMemo(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleAdd();
          }}
          placeholder={t('rightPanel.addMemo')}
          style={{ flex: 1 }}
        />
        <Button variant="primary" size="sm" onClick={handleAdd} disabled={!newMemo.trim()}>
          {t('common.add')}
        </Button>
      </div>
      {memos.length === 0 ? (
        <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
          {t('rightPanel.noMemos')}
        </p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
          {memos.map((memo) => (
            <label
              key={memo.id}
              style={{
                display: 'flex',
                cursor: 'pointer',
                alignItems: 'center',
                gap: 'var(--nf-space-2)',
                borderRadius: 'var(--nf-radius-md)',
                padding: 'var(--nf-space-1) var(--nf-space-2)',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              <input type="checkbox" checked={memo.done} onChange={() => toggleDone(memo.id)} />
              <span
                style={{
                  color: memo.done ? 'var(--nf-color-fg-subtle)' : undefined,
                  textDecoration: memo.done ? 'line-through' : undefined,
                }}
              >
                {memo.text}
              </span>
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

function MembersTab(): ReactElement {
  const { t } = useTranslation();
  const wsId = useWorkspace((s) => s.workspaceId) ?? '';
  const { data: calendars } = useCalendarsQuery();
  const firstShared = calendars?.find((c) => c.kind === 'shared');
  const calendarId = firstShared?.id ?? '';
  const { data: members, isLoading } = useMembersQuery(wsId, calendarId, !!calendarId);

  if (!calendarId) {
    return (
      <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
        {t('rightPanel.noSharedCalendar')}
      </p>
    );
  }

  if (isLoading) {
    return (
      <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
        {t('rightPanel.loadingMembers')}
      </p>
    );
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1)' }}>
      {members?.length === 0 ? (
        <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
          {t('rightPanel.noMembers')}
        </p>
      ) : (
        members?.map((member) => (
          <div
            key={member.userId}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--nf-space-2)',
              borderRadius: 'var(--nf-radius-md)',
              padding: 'var(--nf-space-1) var(--nf-space-2)',
            }}
          >
            <span
              style={{
                display: 'inline-block',
                width: '0.75rem',
                height: '0.75rem',
                flexShrink: 0,
                borderRadius: 'var(--nf-radius-pill)',
                backgroundColor: member.memberColor,
              }}
            />
            <span
              style={{
                flex: 1,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                fontSize: 'var(--nf-text-sm)',
              }}
            >
              {member.displayName}
            </span>
            <Badge tone="neutral">{member.role}</Badge>
          </div>
        ))
      )}
    </div>
  );
}

function ShareTab(): ReactElement {
  const { t } = useTranslation();
  const { data: calendars } = useCalendarsQuery();
  const firstShared = calendars?.find((c) => c.kind === 'shared');

  if (!firstShared) {
    return (
      <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
        {t('rightPanel.noSharedCalendarAvailable')}
      </p>
    );
  }

  const shareUrl = `${window.location.origin}/share/${firstShared.id}`;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-3)' }}>
      <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-muted)' }}>
        {t('rightPanel.shareDescription')}
      </p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
        <Input type="text" readOnly value={shareUrl} style={{ flex: 1 }} />
        <Button
          variant="primary"
          size="sm"
          onClick={() => {
            void navigator.clipboard.writeText(shareUrl);
          }}
        >
          {t('rightPanel.copy')}
        </Button>
      </div>
      <p style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-fg-subtle)' }}>
        {t('rightPanel.shareLinkHint')}
      </p>
    </div>
  );
}

function NotificationsTab(): ReactElement {
  const { t } = useTranslation();
  return (
    <p style={{ fontSize: 'var(--nf-text-sm)', color: 'var(--nf-color-fg-subtle)' }}>
      {t('rightPanel.noNotifications')}
    </p>
  );
}

export default function RightSidebar(): ReactElement | null {
  const { t } = useTranslation();
  const rightPanel = useCalendarUi((s) => s.rightPanel);
  const setRightPanel = useCalendarUi((s) => s.setRightPanel);

  const tabs: { panel: RightPanel; icon: typeof NotebookPen; label: string }[] = [
    { panel: 'memo', icon: NotebookPen, label: t('rightPanel.memo') },
    { panel: 'members', icon: Users, label: t('rightPanel.members') },
    { panel: 'share', icon: Link, label: t('rightPanel.share') },
    { panel: 'notifications', icon: Bell, label: t('rightPanel.notifications') },
  ];

  const panelLabels: Record<RightPanel, string> = {
    memo: t('rightPanel.memo'),
    members: t('rightPanel.members'),
    share: t('rightPanel.share'),
    notifications: t('rightPanel.notifications'),
  };

  if (!rightPanel) return null;

  return (
    <aside
      style={{
        display: 'flex',
        width: '18rem',
        flexShrink: 0,
        flexDirection: 'column',
        borderInlineStart: '1px solid var(--nf-color-border)',
        backgroundColor: 'var(--nf-color-bg-elevated)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          borderBlockEnd: '1px solid var(--nf-color-border)',
          padding: 'var(--nf-space-2) var(--nf-space-3)',
        }}
      >
        <div style={{ display: 'flex', gap: 'var(--nf-space-1)' }}>
          {tabs.map(({ panel, icon: Icon, label }) => (
            <Button
              key={panel}
              variant="ghost"
              size="sm"
              onClick={() => setRightPanel(panel)}
              title={label}
              style={{
                color: rightPanel === panel ? 'var(--nf-color-fg)' : 'var(--nf-color-fg-subtle)',
                backgroundColor: rightPanel === panel ? 'var(--nf-color-bg-sunken)' : undefined,
              }}
            >
              <Icon size={16} />
            </Button>
          ))}
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setRightPanel(null)}
          aria-label={t('rightPanel.close')}
        >
          <X size={16} />
        </Button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 'var(--nf-space-4)' }}>
        <h3
          style={{
            marginBlockEnd: 'var(--nf-space-3)',
            fontSize: 'var(--nf-text-sm)',
            fontWeight: 'var(--nf-weight-semibold)',
            color: 'var(--nf-color-fg)',
          }}
        >
          {panelLabels[rightPanel]}
        </h3>
        {rightPanel === 'memo' ? <MemoTab /> : null}
        {rightPanel === 'members' ? <MembersTab /> : null}
        {rightPanel === 'share' ? <ShareTab /> : null}
        {rightPanel === 'notifications' ? <NotificationsTab /> : null}
      </div>
    </aside>
  );
}
