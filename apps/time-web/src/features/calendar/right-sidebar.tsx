import { Bell, Link, NotebookPen, Users, X } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type RightPanel, useCalendarUiStore } from '../../stores/calendar-ui-store';
import { useWorkspaceStore } from '../../stores/workspace-store';
import { useCalendarsQuery, useMembersQuery } from './api';

interface MemoItem {
  id: string;
  text: string;
  done: boolean;
}

function MemoTab(): ReactElement {
  const { t } = useTranslation();
  const [memos, setMemos] = useState<MemoItem[]>([]);
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
    <div className="space-y-3">
      <div className="flex gap-2">
        <input
          type="text"
          value={newMemo}
          onChange={(e) => setNewMemo(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleAdd();
          }}
          placeholder={t('rightPanel.addMemo')}
          className="flex-1 rounded-md border border-[var(--color-border)] px-2 py-1.5 text-sm focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
          style={{
            backgroundColor: 'var(--color-surface-inset)',
            color: 'var(--color-text-primary)',
          }}
        />
        <button
          type="button"
          onClick={handleAdd}
          disabled={!newMemo.trim()}
          className="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-text-on-accent)] hover:opacity-90 disabled:opacity-50"
        >
          {t('common.add')}
        </button>
      </div>
      {memos.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
          {t('rightPanel.noMemos')}
        </p>
      ) : (
        <div className="space-y-1">
          {memos.map((memo) => (
            <label
              key={memo.id}
              className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-[var(--color-hover)]"
            >
              <input
                type="checkbox"
                checked={memo.done}
                onChange={() => toggleDone(memo.id)}
                className="rounded border-[var(--color-border)]"
              />
              <span
                style={memo.done ? { color: 'var(--color-text-tertiary)' } : undefined}
                className={memo.done ? 'line-through' : ''}
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
  const wsId = useWorkspaceStore((s) => s.workspaceId) ?? '';
  const { data: calendars } = useCalendarsQuery();
  const firstShared = calendars?.find((c) => c.kind === 'shared');
  const calendarId = firstShared?.id ?? '';
  const { data: members, isLoading } = useMembersQuery(wsId, calendarId, !!calendarId);

  if (!calendarId) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
        {t('rightPanel.noSharedCalendar')}
      </p>
    );
  }

  if (isLoading) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-text-secondary)' }}>
        {t('rightPanel.loadingMembers')}
      </p>
    );
  }

  return (
    <div className="space-y-1">
      {members?.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
          {t('rightPanel.noMembers')}
        </p>
      ) : (
        members?.map((member) => (
          <div key={member.userId} className="flex items-center gap-2 rounded-md px-2 py-1.5">
            <span
              className="h-3 w-3 shrink-0 rounded-full"
              style={{ backgroundColor: member.memberColor }}
            />
            <span className="flex-1 truncate text-sm">{member.displayName}</span>
            <span
              className="rounded bg-[var(--color-surface-inset)] px-1.5 py-0.5 text-[10px] font-medium capitalize"
              style={{ color: 'var(--color-text-secondary)' }}
            >
              {member.role}
            </span>
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
      <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
        {t('rightPanel.noSharedCalendarAvailable')}
      </p>
    );
  }

  const shareUrl = `${window.location.origin}/share/${firstShared.id}`;

  return (
    <div className="space-y-3">
      <p className="text-sm" style={{ color: 'var(--color-text-secondary)' }}>
        {t('rightPanel.shareDescription')}
      </p>
      <div className="flex items-center gap-2">
        <input
          type="text"
          readOnly
          value={shareUrl}
          className="flex-1 truncate rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-2 py-1.5 text-sm"
          style={{ color: 'var(--color-text-secondary)' }}
        />
        <button
          type="button"
          onClick={() => {
            void navigator.clipboard.writeText(shareUrl);
          }}
          className="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-text-on-accent)] hover:opacity-90"
        >
          {t('rightPanel.copy')}
        </button>
      </div>
      <p className="text-xs" style={{ color: 'var(--color-text-tertiary)' }}>
        {t('rightPanel.shareLinkHint')}
      </p>
    </div>
  );
}

function NotificationsTab(): ReactElement {
  const { t } = useTranslation();
  return (
    <p className="text-sm" style={{ color: 'var(--color-text-tertiary)' }}>
      {t('rightPanel.noNotifications')}
    </p>
  );
}

export default function RightSidebar(): ReactElement | null {
  const { t } = useTranslation();
  const { rightPanel, setRightPanel } = useCalendarUiStore();

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
      className="flex w-72 shrink-0 flex-col border-l border-[var(--color-border)]"
      style={{ backgroundColor: 'var(--color-surface-elevated)' }}
    >
      <div className="flex items-center justify-between border-b border-[var(--color-border)] px-3 py-2">
        <div className="flex gap-1">
          {tabs.map(({ panel, icon: Icon, label }) => (
            <button
              key={panel}
              type="button"
              onClick={() => setRightPanel(panel)}
              className={`rounded-md p-1.5 ${
                rightPanel === panel
                  ? 'bg-[var(--color-surface-inset)]'
                  : 'hover:bg-[var(--color-hover)]'
              }`}
              style={{
                color:
                  rightPanel === panel ? 'var(--color-text-primary)' : 'var(--color-text-tertiary)',
              }}
              title={label}
            >
              <Icon className="h-4 w-4" />
            </button>
          ))}
        </div>
        <button
          type="button"
          onClick={() => setRightPanel(null)}
          className="rounded-md p-1 hover:bg-[var(--color-hover)]"
          style={{ color: 'var(--color-text-tertiary)' }}
          aria-label={t('rightPanel.close')}
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <h3 className="mb-3 text-sm font-semibold" style={{ color: 'var(--color-text-primary)' }}>
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
