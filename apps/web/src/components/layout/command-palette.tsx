/**
 * CommandPalette — Cmd+K launcher.
 *
 * Minimal keyboard-first palette with a search input, static navigation
 * entries, and a dynamic workspace list. Arrow keys move selection, Enter
 * navigates, Escape closes (via the host Dialog).
 */

import Dialog from '@nodate-flow/ui/primitives/dialog';
import { useQueries } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import {
  type KeyboardEvent,
  type ReactElement,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskListItem } from '../../features/tasks/api';
import { useWorkspacesQuery } from '../../features/workspaces/api';
import { sdk } from '../../lib/sdk';

interface CommandItem {
  id: string;
  label: string;
  group: string;
  href: string;
}

interface InnerProps {
  onSelect: (href: string) => void;
}

function normalize(s: string): string {
  return s.toLowerCase();
}

function PaletteBody({ onSelect }: InnerProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(query.trim()), 180);
    return () => window.clearTimeout(id);
  }, [query]);

  const shouldSearchTasks = debounced.length >= 2;

  const taskQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['command-palette', 'tasks', w.id, debounced] as const,
      enabled: shouldSearchTasks,
      staleTime: 10_000,
      queryFn: async (): Promise<TaskListItem[]> => {
        const { data, error } = await sdk.GET('/tasks', {
          params: { query: { workspaceId: w.id, q: debounced, limit: 10, offset: 0 } },
        });
        if (error || !data) return [];
        return data.tasks ?? [];
      },
    })),
  });

  const taskResults = useMemo<TaskListItem[]>(() => {
    if (!shouldSearchTasks) return [];
    const out: TaskListItem[] = [];
    for (const q of taskQueries) {
      if (q.data) out.push(...q.data);
      if (out.length >= 10) break;
    }
    return out.slice(0, 10);
  }, [shouldSearchTasks, taskQueries]);

  const items = useMemo<CommandItem[]>(() => {
    const navGroup = t('dock.command_palette.group_navigation');
    const wsGroup = t('dock.command_palette.group_workspaces');
    const taskGroup = t('dock.command_palette.group_tasks');
    const nav: CommandItem[] = [
      { id: 'nav:home', label: t('dock.command_palette.home'), group: navGroup, href: '/' },
      { id: 'nav:today', label: t('nav.today'), group: navGroup, href: '/today' },
      { id: 'nav:inbox', label: t('nav.inbox'), group: navGroup, href: '/inbox' },
      { id: 'nav:workspaces', label: t('nav.workspaces'), group: navGroup, href: '/workspaces' },
      { id: 'nav:settings', label: t('nav.settings'), group: navGroup, href: '/settings' },
    ];
    const ws: CommandItem[] = workspaces.map((w) => ({
      id: `ws:${w.id}`,
      label: w.name,
      group: wsGroup,
      href: `/workspaces/${w.id}`,
    }));
    const tasks: CommandItem[] = taskResults.map((task) => ({
      id: `task:${task.id}`,
      label: task.title,
      group: taskGroup,
      href: `/tasks/${task.id}`,
    }));
    return [...tasks, ...nav, ...ws];
  }, [t, workspaces, taskResults]);

  const filtered = useMemo<CommandItem[]>(() => {
    const q = normalize(query.trim());
    if (q === '') return items;
    return items.filter((it) => normalize(it.label).includes(q));
  }, [items, query]);

  useEffect(() => {
    setActive(0);
  }, []);

  useEffect(() => {
    if (active >= filtered.length) setActive(0);
  }, [filtered, active]);

  const grouped = useMemo(() => {
    const map = new Map<string, CommandItem[]>();
    for (const it of filtered) {
      const list = map.get(it.group) ?? [];
      list.push(it);
      map.set(it.group, list);
    }
    return Array.from(map.entries());
  }, [filtered]);

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i + 1) % filtered.length));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i - 1 + filtered.length) % filtered.length));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const picked = filtered[active];
      if (picked) onSelect(picked.href);
    }
  };

  let flatIdx = -1;
  return (
    <div
      style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', minInlineSize: '22rem' }}
    >
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={t('dock.command_palette.placeholder')}
        aria-label={t('dock.command_palette.title')}
        style={{
          padding: '0.5rem 0.75rem',
          borderRadius: '0.375rem',
          border: '1px solid var(--nf-color-border)',
          background: 'var(--nf-color-bg)',
          color: 'var(--nf-color-fg)',
          fontSize: '0.875rem',
        }}
      />
      {filtered.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
          {t('dock.command_palette.empty')}
        </p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
          {grouped.map(([group, its]) => (
            <div key={group}>
              <div
                style={{
                  fontSize: '0.6875rem',
                  textTransform: 'uppercase',
                  letterSpacing: '0.04em',
                  color: 'var(--nf-color-fg-subtle)',
                  padding: '0.25rem 0.5rem',
                }}
              >
                {group}
              </div>
              <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
                {its.map((it) => {
                  flatIdx += 1;
                  const isActive = flatIdx === active;
                  return (
                    <li key={it.id}>
                      <button
                        type="button"
                        aria-current={isActive ? 'true' : undefined}
                        onClick={() => onSelect(it.href)}
                        onMouseEnter={() => {
                          const idx = filtered.findIndex((f) => f.id === it.id);
                          if (idx >= 0) setActive(idx);
                        }}
                        style={{
                          display: 'block',
                          inlineSize: '100%',
                          textAlign: 'start',
                          padding: '0.5rem 0.75rem',
                          borderRadius: '0.375rem',
                          border: 'none',
                          background: isActive ? 'var(--nf-color-surface-hover)' : 'transparent',
                          color: 'var(--nf-color-fg)',
                          cursor: 'pointer',
                          fontSize: '0.875rem',
                        }}
                      >
                        {it.label}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
}

export default function CommandPalette({ open, onClose }: CommandPaletteProps): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();

  const handleSelect = (href: string): void => {
    onClose();
    void navigate({ to: href });
  };

  return (
    <Dialog open={open} onClose={onClose} title={t('dock.command_palette.title')}>
      <Suspense fallback={<p style={{ margin: 0 }}>…</p>}>
        <PaletteBody onSelect={handleSelect} />
      </Suspense>
    </Dialog>
  );
}
