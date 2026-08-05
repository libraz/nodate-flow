/**
 * ArchivedRow — a single row in the Archive list.
 *
 * Renders the row as a `<li>` (not a `<button>`) so we can put the
 * checkbox + the title link inside without nested interactive elements
 * (button-in-button is forbidden by the spec). The whole `<li>` is
 * keyboard-focusable so j/k navigation can land on it; Enter triggers
 * "view detail", Space toggles selection, `u` unarchives.
 *
 * Hover-only actions are driven by CSS opacity so the action cluster
 * stays out of the visual layout when idle but becomes interactive on
 * hover/focus-within. The row also opts into a dynamic Tooltip when its
 * title overflows the available inline-size — the Tooltip primitive
 * already debounces hover.
 */

import Avatar from '@nodate-flow/ui/primitives/avatar';
import Button from '@nodate-flow/ui/primitives/button';
import Checkbox from '@nodate-flow/ui/primitives/checkbox';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { Link } from '@tanstack/react-router';
import {
  type KeyboardEvent,
  type MouseEvent,
  memo,
  type ReactElement,
  type Ref,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskListItem } from '../api';

import styles from './archived.module.css';
import { formatTimeAgo } from './relative-time';

export interface ArchivedRowProps {
  task: TaskListItem;
  selected: boolean;
  removing: boolean;
  focused: boolean;
  locale: string;
  /** Optional archiver display profile, when one can be resolved. */
  archiverName?: string;
  archiverAvatarUrl?: string;
  onToggleSelect: (id: string, opts?: { shift?: boolean }) => void;
  onUnarchive: (id: string) => void;
  onActivate: (id: string) => void;
  onFocus: (id: string) => void;
  /** Forwarded to the `<li>` root for keyboard-driven focus management. */
  rowRef?: Ref<HTMLLIElement>;
}

function ArchivedRowImpl({
  task,
  selected,
  removing,
  focused,
  locale,
  archiverName,
  archiverAvatarUrl,
  onToggleSelect,
  onUnarchive,
  onActivate,
  onFocus,
  rowRef,
}: ArchivedRowProps): ReactElement {
  const { t } = useTranslation('archive');
  const titleRef = useRef<HTMLAnchorElement>(null);
  const [truncated, setTruncated] = useState(false);

  // Detect title truncation through ResizeObserver so the Tooltip only
  // wraps the link when text actually overflows. task.title is listed
  // because the effect should re-measure whenever the visible title
  // content changes (which Biome considers redundant given the
  // ResizeObserver fallback, but the observer alone does not fire on
  // pure text-content swaps).
  // biome-ignore lint/correctness/useExhaustiveDependencies: task.title is the intentional trigger
  useEffect(() => {
    const node = titleRef.current;
    if (!node) return;
    const update = (): void => {
      setTruncated(node.scrollWidth > node.clientWidth);
    };
    update();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      update();
    });
    observer.observe(node);
    return () => {
      observer.disconnect();
    };
  }, [task.title]);

  const handleRowClick = (event: MouseEvent<HTMLLIElement>): void => {
    if ((event.target as HTMLElement).closest('a, button, input')) return;
    onActivate(task.id);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLLIElement>): void => {
    if (event.key === ' ') {
      event.preventDefault();
      onToggleSelect(task.id, { shift: event.shiftKey });
      return;
    }
    if (event.key === 'Enter') {
      if ((event.target as HTMLElement).closest('a, button')) return;
      event.preventDefault();
      onActivate(task.id);
      return;
    }
    if (event.key === 'u') {
      event.preventDefault();
      onUnarchive(task.id);
    }
  };

  const ago = formatTimeAgo(task.archivedAt, locale);
  const titleNode = (
    <Link
      ref={titleRef}
      to="/tasks/$taskId"
      params={{ taskId: task.id }}
      className={styles.rowTitle}
      dir="auto"
      onClick={(event) => {
        event.stopPropagation();
      }}
    >
      {task.title}
    </Link>
  );

  const archiverInitial = archiverName ? archiverName.slice(0, 2).toUpperCase() : '';

  return (
    <li
      ref={rowRef}
      className={styles.row}
      data-selected={selected ? 'true' : 'false'}
      data-removing={removing ? 'true' : undefined}
      data-focused={focused ? 'true' : undefined}
      tabIndex={0}
      onClick={handleRowClick}
      onKeyDown={handleKeyDown}
      onFocus={() => {
        onFocus(task.id);
      }}
    >
      <Checkbox
        aria-label={t('row.selectAria', { title: task.title })}
        checked={selected}
        onChange={(event) => {
          // The native change event does not surface shiftKey reliably,
          // so we fall back to the underlying DOM MouseEvent that fired
          // the change. The cast hops through `unknown` because the
          // generic React event's `nativeEvent` type does not narrow to
          // `MouseEvent` in all browsers.
          const native = event.nativeEvent as unknown as { shiftKey?: boolean };
          const shift = native.shiftKey === true;
          onToggleSelect(task.id, { shift });
        }}
        onClick={(event) => {
          event.stopPropagation();
        }}
      />

      {truncated ? <Tooltip content={task.title}>{titleNode}</Tooltip> : titleNode}

      <span className={styles.rowMeta}>
        {archiverName ? (
          <Avatar
            size="sm"
            alt={archiverName}
            initials={archiverInitial}
            {...(archiverAvatarUrl !== undefined ? { src: archiverAvatarUrl } : {})}
          />
        ) : null}
      </span>

      <span className={styles.rowProject}>{task.projectName ?? task.projectIdentifier ?? ''}</span>

      <span className={styles.rowAgo}>{ago ? t('row.archivedAgo', { ago }) : ''}</span>

      <span className={styles.rowActions}>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={(event) => {
            event.stopPropagation();
            onUnarchive(task.id);
          }}
        >
          {t('row.unarchive')}
        </Button>
        <Link
          to="/tasks/$taskId"
          params={{ taskId: task.id }}
          aria-label={t('row.viewDetail')}
          onClick={(event) => {
            event.stopPropagation();
          }}
        >
          <Button type="button" variant="ghost" size="sm">
            {t('row.viewDetail')}
          </Button>
        </Link>
      </span>
    </li>
  );
}

/**
 * Memoized so the virtualized list does not re-render every visible
 * row when an unrelated row's selection / focus state flips.
 */
const ArchivedRow = memo(ArchivedRowImpl);
ArchivedRow.displayName = 'ArchivedRow';

export default ArchivedRow;
