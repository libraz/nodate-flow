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
import { cx } from '@nodate-flow/ui/lib/cx';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { useQueries } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import {
  Archive,
  Building2,
  CalendarDays,
  CalendarRange,
  FileText,
  FolderKanban,
  Home,
  Inbox,
  type LucideIcon,
  NotebookPen,
  Plus,
  Settings,
  SquareCheckBig,
} from 'lucide-react';
import {
  type KeyboardEvent,
  type MutableRefObject,
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
import type { DispatchOutcome } from '../../features/nl-command/dispatch';
import { dispatchToolCall, isDispatchableTool } from '../../features/nl-command/dispatch';
import type { Project } from '../../features/projects/api';
import type { TaskListItem } from '../../features/tasks/api';
import { useWorkspacesQuery } from '../../features/workspaces/api';
import { apiRequest } from '../../lib/api';
import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import css from './command-palette.module.css';

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
  /** Switches the palette back to search mode with the given query. */
  onSearch: (query: string) => void;
  /**
   * Receives the Enter-key handler so the parent input can invoke the
   * command-mode submit/execute flow. Keydown events don't bubble from the
   * input to this sibling wrapper, so routing through a ref is required.
   */
  submitHandlerRef: MutableRefObject<(() => void) | null>;
}

/**
 * Renders the outcome of a dispatch. Only the two navigating outcomes
 * leave the palette; every refusal stays on screen and says what stopped
 * it, so a command that did nothing never looks like one that worked.
 */
function outcomeMessage(
  outcome: DispatchOutcome,
  t: ReturnType<typeof useTranslation<'common'>>['t'],
): string {
  switch (outcome.kind) {
    case 'unsupported':
      return t('dock.command_palette.unsupported', { tool: outcome.tool });
    case 'unresolved':
      switch (outcome.reason) {
        case 'missing':
          return t('dock.command_palette.unresolved_missing', { argument: outcome.argument });
        case 'not_found':
          return t('dock.command_palette.unresolved_not_found', { term: outcome.term });
        default:
          return t('dock.command_palette.unresolved_ambiguous', { term: outcome.term });
      }
    default:
      return t('dock.command_palette.execute_failed');
  }
}

function CommandModeBody({
  prompt,
  wsId,
  onSelect,
  onSearch,
  submitHandlerRef,
}: CommandModeBodyProps): ReactElement {
  const { t } = useTranslation('common');
  const resolveCommand = useResolveCommand(wsId);
  const [result, setResult] = useState<ResolveCommandResult | null>(null);
  const [outcome, setOutcome] = useState<DispatchOutcome | null>(null);
  const [executing, setExecuting] = useState(false);

  // Track the last submitted prompt to avoid re-submitting on every render
  const lastSubmittedRef = useRef<string>('');

  const handleSubmit = (): void => {
    if (!wsId || prompt.length === 0) return;
    if (lastSubmittedRef.current === prompt && result) return;
    lastSubmittedRef.current = prompt;
    setResult(null);
    setOutcome(null);
    resolveCommand.mutate(prompt, {
      onSuccess: (data) => setResult(data),
    });
  };

  const handleExecute = (): void => {
    if (!result || !wsId || executing) return;
    setOutcome(null);
    setExecuting(true);
    void dispatchToolCall(result, { workspaceId: wsId }).then((next) => {
      setExecuting(false);
      if (next.kind === 'executed' || next.kind === 'navigated') {
        onSelect(next.navigateTo);
        return;
      }
      if (next.kind === 'search') {
        onSearch(next.query);
        return;
      }
      setOutcome(next);
    });
  };

  // Expose Enter handling to the parent input. Sync on every render so the
  // handler closes over the current `result` / `resolveCommand.isPending`.
  submitHandlerRef.current = (): void => {
    if (result) {
      handleExecute();
    } else if (!resolveCommand.isPending) {
      handleSubmit();
    }
  };

  if (!wsId) {
    return <p className={css.emptyText}>{t('dock.command_palette.no_workspace')}</p>;
  }

  return (
    <div className={css.commandModeBody}>
      <div className={css.groupLabel}>{t('dock.command_palette.group_command')}</div>

      {resolveCommand.isPending && (
        <p
          className={css.emptyText}
          style={{ padding: 'var(--nf-space-2) var(--nf-space-3)' }}
          aria-live="polite"
        >
          {t('dock.command_palette.resolving')}
        </p>
      )}

      {resolveCommand.isError && (
        <p
          className={css.emptyText}
          style={{
            padding: 'var(--nf-space-2) var(--nf-space-3)',
            color: 'var(--nf-color-danger-fg)',
          }}
          aria-live="assertive"
        >
          {t('dock.command_palette.error')}
        </p>
      )}

      {!resolveCommand.isPending && !result && !resolveCommand.isError && (
        <p className={css.emptyText} style={{ padding: 'var(--nf-space-2) var(--nf-space-3)' }}>
          {prompt.length > 0
            ? t('dock.command_palette.hint_select')
            : t('dock.command_palette.placeholder_command')}
        </p>
      )}

      {result && (
        <div className={css.commandResult}>
          <div className={css.commandToolRow}>
            <span className={css.commandToolName}>{result.tool}</span>
            <span
              className={cx(
                css.confidenceBadge,
                result.confidence >= 0.8 ? css.confidenceHigh : css.confidenceLow,
              )}
            >
              {t('dock.command_palette.confidence', {
                score: String(Math.round(result.confidence * 100)),
              })}
            </span>
          </div>

          <pre className={css.commandArgs}>{JSON.stringify(result.args, null, 2)}</pre>

          {/* A tool with no handler says so before Enter is pressed, so
              the palette never offers to run something it cannot run. */}
          {!isDispatchableTool(result.tool) ? (
            <p
              className={css.emptyText}
              style={{ color: 'var(--nf-color-danger-fg)' }}
              aria-live="assertive"
            >
              {t('dock.command_palette.unsupported', { tool: result.tool })}
            </p>
          ) : executing ? (
            <p className={css.emptyText} style={{ color: 'var(--nf-color-fg)' }} aria-live="polite">
              {t('dock.command_palette.executing')}
            </p>
          ) : outcome ? (
            <p
              className={css.emptyText}
              style={{ color: 'var(--nf-color-danger-fg)' }}
              aria-live="assertive"
            >
              {outcomeMessage(outcome, t)}
            </p>
          ) : (
            <p className={css.emptyText} style={{ color: 'var(--nf-color-fg)' }} aria-live="polite">
              {result.confidence >= 0.8
                ? t('dock.command_palette.confirm_execute')
                : t('dock.command_palette.confirm_low', { tool: result.tool })}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Search mode sub-component (extracted from PaletteBody)
// ---------------------------------------------------------------------------

function PaletteBody({ onSelect, initialCommandMode }: InnerProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: workspaces } = useWorkspacesQuery();
  // Fall back to the sole workspace when the route carries no active
  // workspace id (e.g. on `/`, `/today`, `/inbox` with a fresh session).
  // Matches the top-bar switcher's single-workspace auto-select pattern.
  const wsId =
    useCurrentWorkspaceId() ?? (workspaces.length === 1 ? (workspaces[0]?.id ?? null) : null);
  const [query, setQuery] = useState(initialCommandMode ? '> ' : '');
  const [debounced, setDebounced] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  // Populated by CommandModeBody so the input's Enter handler can dispatch
  // the NL submit/execute flow. See CommandModeBodyProps.submitHandlerRef.
  const commandSubmitRef = useRef<(() => void) | null>(null);

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
        // One workspace refusing the search contributes no rows; the
        // palette still answers from the workspaces that replied.
        const data = await apiRequest(
          (client) =>
            client.GET('/tasks', {
              params: { query: { workspaceId: w.id, q: debounced, limit: 10, offset: 0 } },
            }),
          'Failed to search tasks',
          { onError: 'empty', empty: null },
        );
        return data?.tasks ?? [];
      },
    })),
  });

  const projectQueries = useQueries({
    queries: workspaces.map((w) => ({
      queryKey: ['command-palette', 'projects', w.id] as const,
      staleTime: 60_000,
      queryFn: async (): Promise<Array<Project & { workspaceId: string }>> => {
        const data = await apiRequest(
          (client) =>
            client.GET('/workspaces/{wsId}/projects', { params: { path: { wsId: w.id } } }),
          'Failed to load projects',
          { onError: 'empty', empty: null },
        );
        return (data?.projects ?? []).map((p) => ({ ...p, workspaceId: w.id }));
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
      href: `/workspaces/${p.workspaceId}/projects/${p.id}/tasks`,
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
        id: 'nav:calendar',
        label: t('nav.calendar'),
        group: navGroup,
        href: '/calendar',
        icon: CalendarRange,
      },
      {
        id: 'nav:pages',
        label: t('nav.pages'),
        group: navGroup,
        href: '/pages',
        icon: FileText,
      },
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
    // Workspace-scoped destinations, which only resolve once a workspace
    // is in context. The archive and the retro-draft queue have no fixed
    // path, so the palette is the one place they can be reached by name.
    const workspaceNav: CommandItem[] = wsId
      ? [
          {
            id: 'nav:retro-drafts',
            label: t('nav.retroDrafts'),
            group: navGroup,
            href: `/workspaces/${wsId}/tasks/drafts`,
            icon: NotebookPen,
          },
          {
            id: 'nav:archive',
            label: t('nav.archive'),
            group: navGroup,
            href: `/workspaces/${wsId}/tasks/archived`,
            icon: Archive,
          },
        ]
      : [];
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
      href: `/workspaces/${p.workspaceId}/projects/${p.id}`,
      icon: FolderKanban,
    }));
    return [...actions, ...tasks, ...projects, ...nav, ...workspaceNav, ...ws];
  }, [t, wsId, workspaces, taskResults, projectResults]);

  const filtered = useMemo<CommandItem[]>(() => {
    if (mode === 'command') return [];
    const q = normalize(query.trim());
    if (q === '') return items;
    return items.filter((it) => normalize(it.label).includes(q));
  }, [items, query, mode]);

  const isSearching = shouldSearchTasks && taskQueries.some((q) => q.isLoading || q.isFetching);

  const filteredLen = filtered.length;
  useEffect(() => {
    setActive((prev) => Math.min(prev, Math.max(filteredLen - 1, 0)));
  }, [filteredLen]);

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
      // Keydown events don't bubble from <input> to the sibling
      // CommandModeBody wrapper, so dispatch the command-mode submit
      // handler directly via the ref it populates.
      if (e.key === 'Enter') {
        e.preventDefault();
        commandSubmitRef.current?.();
      }
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
    <div className={css.body}>
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
        className={css.input}
      />
      {mode === 'command' ? (
        <CommandModeBody
          prompt={commandPrompt}
          wsId={wsId}
          onSelect={onSelect}
          onSearch={(q) => {
            setQuery(q);
            setActive(0);
          }}
          submitHandlerRef={commandSubmitRef}
        />
      ) : isSearching && filtered.length === 0 ? (
        <p className={css.emptyText} aria-live="polite">
          {t('dock.command_palette.searching')}
        </p>
      ) : filtered.length === 0 ? (
        <p className={css.emptyText}>{t('dock.command_palette.empty')}</p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
          {grouped.map(([group, its]) => (
            <div key={group}>
              <div className={css.groupLabel}>{group}</div>
              <ul className={css.resultList}>
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
                        className={cx(css.resultItem, isActive && css.resultItemActive)}
                      >
                        {it.icon ? (
                          <Icon icon={it.icon} decorative style={{ flexShrink: 0, opacity: 0.6 }} />
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
      <div className={css.hintBar}>
        <span className={css.hintGroup}>
          <kbd className={css.kbd}>↑</kbd>
          <kbd className={css.kbd}>↓</kbd>
          {t('dock.command_palette.hint_nav')}
        </span>
        <span className={css.hintGroup}>
          <kbd className={css.kbd}>↵</kbd>
          {t('dock.command_palette.hint_select')}
        </span>
        <span className={css.hintGroup}>
          <kbd className={css.kbd}>Esc</kbd>
          {t('dock.command_palette.hint_close')}
        </span>
        {mode === 'search' && (
          <span className={css.hintGroupEnd}>
            <kbd className={css.kbd}>&gt;</kbd>
            {t('dock.command_palette.command_hint')}
          </span>
        )}
      </div>
    </div>
  );
}

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
