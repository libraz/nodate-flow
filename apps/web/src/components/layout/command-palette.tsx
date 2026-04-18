/**
 * CommandPalette -- Cmd+K launcher.
 *
 * Minimal keyboard-first palette with a search input, static navigation
 * entries, and a dynamic workspace list. Arrow keys move selection, Enter
 * navigates, Escape closes (via the host Dialog).
 *
 * When the input starts with `>`, the palette switches to NL command
 * mode: the user types a natural language command and the AI
 * resolve-command endpoint translates it into a tool invocation.
 */

import Icon from '@nodate-flow/ui/icon';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { useQueries } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import {
  Building2,
  CalendarDays,
  FolderKanban,
  Home,
  Inbox,
  type LucideIcon,
  Plus,
  Settings,
  SquareCheckBig,
} from 'lucide-react';
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

import type { ResolveCommandResult } from '../../features/nl-command/api';
import { useResolveCommand } from '../../features/nl-command/api';
import type { Project } from '../../features/projects/api';
import type { TaskListItem } from '../../features/tasks/api';
import { useWorkspacesQuery } from '../../features/workspaces/api';
import { sdk } from '../../lib/sdk';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';

interface CommandItem {
  id: string;
  label: string;
  group: string;
  href: string;
  search?: Record<string, unknown>;
  icon?: LucideIcon;
}

type PaletteMode = 'search' | 'command';

interface InnerProps {
  onSelect: (item: Pick<CommandItem, 'href' | 'search'>) => void;
  initialCommandMode?: boolean | undefined;
}

function normalize(s: string): string {
  return s.toLowerCase();
}

/** Strips the leading `> ` prefix from a command-mode query. */
function stripCommandPrefix(raw: string): string {
  return raw.replace(/^>\s*/, '');
}

// ---------------------------------------------------------------------------
// NL command mode sub-component
// ---------------------------------------------------------------------------

interface CommandModeBodyProps {
  prompt: string;
  wsId: string | null;
  onSelect: InnerProps['onSelect'];
}

function CommandModeBody({ prompt, wsId, onSelect }: CommandModeBodyProps): ReactElement {
  const { t } = useTranslation('common');
  const resolveCommand = useResolveCommand(wsId);
  const [result, setResult] = useState<ResolveCommandResult | null>(null);

  // Track the last submitted prompt to avoid re-submitting on every render
  const lastSubmittedRef = useRef<string>('');

  const handleSubmit = (): void => {
    if (!wsId || prompt.length === 0) return;
    if (lastSubmittedRef.current === prompt && result) return;
    lastSubmittedRef.current = prompt;
    setResult(null);
    resolveCommand.mutate(prompt, {
      onSuccess: (data) => setResult(data),
    });
  };

  const handleExecute = (): void => {
    if (!result) return;
    // Map resolved tool to a navigation action
    const href = toolToHref(result);
    if (href) {
      onSelect(href);
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (result) {
        handleExecute();
      } else if (!resolveCommand.isPending) {
        handleSubmit();
      }
    }
  };

  if (!wsId) {
    return (
      <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: '0.875rem' }}>
        {t('dock.command_palette.no_workspace')}
      </p>
    );
  }

  return (
    // biome-ignore lint/a11y/noNoninteractiveTabindex: keyboard trap for command result
    <div onKeyDown={handleKeyDown} tabIndex={0} style={{ outline: 'none' }}>
      <div
        style={{
          fontSize: '0.6875rem',
          textTransform: 'uppercase',
          letterSpacing: '0.04em',
          color: 'var(--nf-color-fg-subtle)',
          padding: '0.25rem 0.5rem',
        }}
      >
        {t('dock.command_palette.group_command')}
      </div>

      {resolveCommand.isPending && (
        <p
          style={{
            margin: 0,
            padding: '0.5rem 0.75rem',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.875rem',
          }}
          aria-live="polite"
        >
          {t('dock.command_palette.resolving')}
        </p>
      )}

      {resolveCommand.isError && (
        <p
          style={{
            margin: 0,
            padding: '0.5rem 0.75rem',
            color: 'var(--nf-color-danger)',
            fontSize: '0.875rem',
          }}
          aria-live="assertive"
        >
          {t('dock.command_palette.error')}
        </p>
      )}

      {!resolveCommand.isPending && !result && !resolveCommand.isError && (
        <p
          style={{
            margin: 0,
            padding: '0.5rem 0.75rem',
            color: 'var(--nf-color-fg-muted)',
            fontSize: '0.875rem',
          }}
        >
          {prompt.length > 0
            ? t('dock.command_palette.hint_select')
            : t('dock.command_palette.placeholder_command')}
        </p>
      )}

      {result && (
        <div
          style={{
            padding: '0.5rem 0.75rem',
            display: 'flex',
            flexDirection: 'column',
            gap: '0.5rem',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <span
              style={{
                fontWeight: 600,
                fontSize: '0.875rem',
                color: 'var(--nf-color-fg)',
              }}
            >
              {result.tool}
            </span>
            <span
              style={{
                fontSize: '0.6875rem',
                padding: '0.125rem 0.375rem',
                borderRadius: '0.25rem',
                background:
                  result.confidence >= 0.8
                    ? 'var(--nf-color-success-subtle, oklch(0.85 0.15 145))'
                    : 'var(--nf-color-warning-subtle, oklch(0.85 0.15 85))',
                color:
                  result.confidence >= 0.8
                    ? 'var(--nf-color-success-fg, oklch(0.35 0.15 145))'
                    : 'var(--nf-color-warning-fg, oklch(0.35 0.15 85))',
              }}
            >
              {t('dock.command_palette.confidence', {
                score: String(Math.round(result.confidence * 100)),
              })}
            </span>
          </div>

          <pre
            style={{
              margin: 0,
              fontSize: '0.75rem',
              color: 'var(--nf-color-fg-muted)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              fontFamily: 'var(--font-mono, monospace)',
            }}
          >
            {JSON.stringify(result.args, null, 2)}
          </pre>

          <p
            style={{
              margin: 0,
              fontSize: '0.8125rem',
              color: 'var(--nf-color-fg)',
            }}
            aria-live="polite"
          >
            {result.confidence >= 0.8
              ? t('dock.command_palette.confirm_execute')
              : t('dock.command_palette.confirm_low', { tool: result.tool })}
          </p>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Tool -> navigation mapping
// ---------------------------------------------------------------------------

function toolToHref(result: ResolveCommandResult): Pick<CommandItem, 'href' | 'search'> | null {
  const args = result.args;
  switch (result.tool) {
    case 'create_task': {
      const projectId = typeof args.projectId === 'string' ? args.projectId : null;
      if (projectId) {
        return {
          href: `/projects/${projectId}/tasks`,
          search: { new: true, ...(typeof args.title === 'string' ? { title: args.title } : {}) },
        };
      }
      return { href: '/today', search: { new: true } };
    }
    case 'navigate': {
      const target = typeof args.path === 'string' ? args.path : '/';
      return { href: target };
    }
    default:
      return { href: '/' };
  }
}

// ---------------------------------------------------------------------------
// Search mode sub-component (extracted from PaletteBody)
// ---------------------------------------------------------------------------

function PaletteBody({ onSelect, initialCommandMode }: InnerProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  const wsId = useCurrentWorkspaceId();
  const [query, setQuery] = useState(initialCommandMode ? '> ' : '');
  const [debounced, setDebounced] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const mode: PaletteMode = query.startsWith('>') ? 'command' : 'search';
  const commandPrompt = mode === 'command' ? stripCommandPrefix(query) : '';

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    if (mode === 'command') return;
    const id = window.setTimeout(() => setDebounced(query.trim()), 180);
    return () => window.clearTimeout(id);
  }, [query, mode]);

  const shouldSearchTasks = mode === 'search' && debounced.length >= 2;

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

  const projectQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['command-palette', 'projects', w.id] as const,
      staleTime: 60_000,
      queryFn: async (): Promise<Array<Project & { workspaceId: string }>> => {
        const { data, error } = await sdk.GET('/workspaces/{wsId}/projects', {
          params: { path: { wsId: w.id } },
        });
        if (error || !data) return [];
        return (data.projects ?? []).map((p) => ({ ...p, workspaceId: w.id }));
      },
    })),
  });

  const projectResults = useMemo<Array<Project & { workspaceId: string }>>(() => {
    const out: Array<Project & { workspaceId: string }> = [];
    for (const q of projectQueries) {
      if (q.data) out.push(...q.data);
    }
    return out;
  }, [projectQueries]);

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
    const actionsGroup = t('dock.command_palette.group_actions');
    const navGroup = t('dock.command_palette.group_navigation');
    const wsGroup = t('dock.command_palette.group_workspaces');
    const taskGroup = t('dock.command_palette.group_tasks');
    const projectGroup = t('dock.command_palette.group_projects');
    // One "Create new task" entry per project so the user lands in the
    // right TaskCreateDialog context instead of a dead-end /inbox page.
    // If no projects exist yet, omit the action entirely rather than
    // showing a button that goes nowhere useful.
    const actions: CommandItem[] = projectResults.map((p) => ({
      id: `action:create_task:${p.id}`,
      label:
        projectResults.length === 1
          ? t('dock.command_palette.create_task')
          : `${t('dock.command_palette.create_task')} · ${p.name}`,
      group: actionsGroup,
      href: `/projects/${p.id}/tasks`,
      search: { new: true },
      icon: Plus,
    }));
    const nav: CommandItem[] = [
      {
        id: 'nav:home',
        label: t('dock.command_palette.home'),
        group: navGroup,
        href: '/',
        icon: Home,
      },
      {
        id: 'nav:today',
        label: t('nav.today'),
        group: navGroup,
        href: '/today',
        icon: CalendarDays,
      },
      { id: 'nav:inbox', label: t('nav.inbox'), group: navGroup, href: '/inbox', icon: Inbox },
      {
        id: 'nav:workspaces',
        label: t('nav.workspaces'),
        group: navGroup,
        href: '/workspaces',
        icon: Building2,
      },
      {
        id: 'nav:settings',
        label: t('nav.settings'),
        group: navGroup,
        href: '/settings',
        icon: Settings,
      },
    ];
    const ws: CommandItem[] = workspaces.map((w) => ({
      id: `ws:${w.id}`,
      label: w.name,
      group: wsGroup,
      href: `/workspaces/${w.id}`,
      icon: Building2,
    }));
    const tasks: CommandItem[] = taskResults.map((task) => ({
      id: `task:${task.id}`,
      label: task.title,
      group: taskGroup,
      href: `/tasks/${task.id}`,
      icon: SquareCheckBig,
    }));
    const projects: CommandItem[] = projectResults.map((p) => ({
      id: `project:${p.id}`,
      label: p.name,
      group: projectGroup,
      href: `/projects/${p.id}`,
      icon: FolderKanban,
    }));
    return [...actions, ...tasks, ...projects, ...nav, ...ws];
  }, [t, workspaces, taskResults, projectResults]);

  const filtered = useMemo<CommandItem[]>(() => {
    if (mode === 'command') return [];
    const q = normalize(query.trim());
    if (q === '') return items;
    return items.filter((it) => normalize(it.label).includes(q));
  }, [items, query, mode]);

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
    if (mode === 'command') {
      // In command mode, Enter is handled by CommandModeBody
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i + 1) % filtered.length));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (filtered.length === 0 ? 0 : (i - 1 + filtered.length) % filtered.length));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const picked = filtered[active];
      if (picked) onSelect(picked);
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
        placeholder={
          mode === 'command'
            ? t('dock.command_palette.placeholder_command')
            : t('dock.command_palette.placeholder')
        }
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

      {mode === 'command' ? (
        <CommandModeBody prompt={commandPrompt} wsId={wsId} onSelect={onSelect} />
      ) : (
        <>
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
                            onClick={() => onSelect(it)}
                            onMouseEnter={() => {
                              const idx = filtered.findIndex((f) => f.id === it.id);
                              if (idx >= 0) setActive(idx);
                            }}
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: '0.5rem',
                              inlineSize: '100%',
                              textAlign: 'start',
                              padding: '0.5rem 0.75rem',
                              borderRadius: '0.375rem',
                              border: 'none',
                              background: isActive
                                ? 'var(--nf-color-surface-hover)'
                                : 'transparent',
                              color: 'var(--nf-color-fg)',
                              cursor: 'pointer',
                              fontSize: '0.875rem',
                            }}
                          >
                            {it.icon ? (
                              <Icon
                                icon={it.icon}
                                decorative
                                style={{ flexShrink: 0, opacity: 0.6 }}
                              />
                            ) : null}
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
        </>
      )}

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '0.875rem',
          paddingBlockStart: '0.5rem',
          borderBlockStart: '1px solid var(--nf-color-border)',
          fontSize: '0.6875rem',
          color: 'var(--nf-color-fg-subtle, var(--color-muted))',
        }}
      >
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '0.375rem' }}>
          <kbd style={kbdStyle}>↑</kbd>
          <kbd style={kbdStyle}>↓</kbd>
          {t('dock.command_palette.hint_nav')}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '0.375rem' }}>
          <kbd style={kbdStyle}>↵</kbd>
          {t('dock.command_palette.hint_select')}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: '0.375rem' }}>
          <kbd style={kbdStyle}>Esc</kbd>
          {t('dock.command_palette.hint_close')}
        </span>
        {mode === 'search' && (
          <span
            style={{
              marginInlineStart: 'auto',
              display: 'inline-flex',
              alignItems: 'center',
              gap: '0.375rem',
            }}
          >
            <kbd style={kbdStyle}>&gt;</kbd>
            {t('dock.command_palette.command_hint')}
          </span>
        )}
      </div>
    </div>
  );
}

const kbdStyle = {
  fontFamily: 'var(--font-mono, monospace)',
  fontSize: '0.6875rem',
  padding: '0.0625rem 0.375rem',
  borderRadius: '0.25rem',
  border: '1px solid var(--nf-color-border)',
  background: 'var(--nf-color-surface, transparent)',
  color: 'var(--nf-color-fg, var(--color-fg))',
  lineHeight: 1.4,
} as const;

export interface CommandPaletteProps {
  open: boolean;
  onClose: () => void;
  /** When true the palette opens in NL command mode ("> " prefilled). */
  initialCommandMode?: boolean;
}

export default function CommandPalette({
  open,
  onClose,
  initialCommandMode,
}: CommandPaletteProps): ReactElement {
  const { t } = useTranslation('common');
  const navigate = useNavigate();

  const handleSelect = (item: { href: string; search?: Record<string, unknown> }): void => {
    onClose();
    // `to` is typed as a specific literal union in TanStack Router, but
    // here we are building hrefs dynamically from workspace / project /
    // task ids, so a cast is unavoidable.
    void navigate({
      to: item.href as never,
      ...(item.search ? { search: item.search as never } : {}),
    });
  };

  return (
    <Dialog open={open} onClose={onClose} title={t('dock.command_palette.title')}>
      <Suspense fallback={<p style={{ margin: 0 }}>…</p>}>
        <PaletteBody onSelect={handleSelect} initialCommandMode={initialCommandMode} />
      </Suspense>
    </Dialog>
  );
}
