/**
 * ArchivedList — virtualized list of archived tasks grouped into
 * editorial "chapter" strata.
 *
 * Implementation
 * --------------
 * The list is built by flattening the chapter groups produced by
 * {@link useTimeStrata} into a single ordered array of items, where
 * each item is either a `chapter` header or a `row`. We hand that
 * array to `@tanstack/react-virtual` so only on-screen rows mount.
 *
 * Sticky chapter heading
 * ----------------------
 * `position: sticky` does not behave cleanly inside a virtualizer
 * because the virtual rows are absolutely positioned via `transform`.
 * Instead we render every chapter header as a regular virtual row —
 * `useTimeStrata` already groups in chronological order, so chapters
 * naturally appear above their rows — and additionally render a
 * single overlay header pinned to the top of the scroll viewport.
 * The overlay tracks the topmost virtual item that is currently
 * either a `chapter` row or whose row belongs to a chapter; when the
 * user scrolls past one chapter into the next the overlay swaps
 * label without remounting the list.
 *
 * Keyboard navigation
 * -------------------
 * - `j` / `ArrowDown`  → focus next row
 * - `k` / `ArrowUp`    → focus previous row
 * - `Enter`            → activate (handled inside `ArchivedRow`)
 * - `Space`            → toggle selection (handled inside `ArchivedRow`)
 * - `u`                → unarchive focused row (handled inside `ArchivedRow`)
 * - `U` (shift+u)      → unarchive all selected rows (forwarded up via prop)
 *
 * The list itself listens for j/k/ArrowDown/ArrowUp at the container
 * level so navigation works even when focus is in a nested control,
 * mirroring the inbox / today list pattern.
 */

import { useVirtualizer } from '@tanstack/react-virtual';
import {
  type KeyboardEvent,
  type ReactElement,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import type { TaskListItem } from '../api';
import styles from './archived.module.css';
import ArchivedRow from './archived-row';
import type { ChapterGroup, ChapterId } from './hooks/use-time-strata';

const ROW_HEIGHT = 48;
const CHAPTER_HEIGHT = 40;
const OVERSCAN = 8;

type FlatItem =
  | { kind: 'chapter'; chapterId: ChapterId; count: number; key: string }
  | { kind: 'row'; chapterId: ChapterId; task: TaskListItem; key: string };

export interface ArchivedListProps {
  groups: readonly ChapterGroup[];
  /** Visible row order (flattened across chapters). Used for nav and selection. */
  orderedIds: readonly string[];
  selected: ReadonlySet<string>;
  removing: ReadonlySet<string>;
  archiverNameById: ReadonlyMap<string, string>;
  archiverAvatarById: ReadonlyMap<string, string>;
  locale: string;
  /** Whether another page is available from the API. */
  hasNextPage: boolean;
  /** Whether the next page is in flight. */
  isFetchingNextPage: boolean;
  /** Request the next page; called when scrolled near the end. */
  onLoadMore: () => void;
  onToggleSelect: (id: string, opts?: { shift?: boolean }) => void;
  onUnarchive: (id: string) => void;
  onUnarchiveSelected: () => void;
  onActivate: (id: string) => void;
}

/** Flatten chapter groups into a single ordered virtualizer feed. */
function flatten(groups: readonly ChapterGroup[]): FlatItem[] {
  const out: FlatItem[] = [];
  for (const group of groups) {
    out.push({
      kind: 'chapter',
      chapterId: group.id,
      count: group.rows.length,
      key: `chapter-${group.id}`,
    });
    for (const row of group.rows) {
      out.push({
        kind: 'row',
        chapterId: group.id,
        task: row,
        key: `row-${row.id}`,
      });
    }
  }
  return out;
}

export default function ArchivedList({
  groups,
  orderedIds,
  selected,
  removing,
  archiverNameById,
  archiverAvatarById,
  locale,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  onToggleSelect,
  onUnarchive,
  onUnarchiveSelected,
  onActivate,
}: ArchivedListProps): ReactElement {
  const { t } = useTranslation('archive');
  const parentRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef<Map<string, HTMLLIElement>>(new Map());
  const [focusedId, setFocusedId] = useState<string | null>(null);
  const [stickyChapter, setStickyChapter] = useState<ChapterId | null>(groups[0]?.id ?? null);

  const items = useMemo(() => flatten(groups), [groups]);

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (index: number): number => {
      const item = items[index];
      return item?.kind === 'chapter' ? CHAPTER_HEIGHT : ROW_HEIGHT;
    },
    overscan: OVERSCAN,
    getItemKey: (index) => items[index]?.key ?? String(index),
  });

  // Prefetch the next page when the virtualizer's last visible item is
  // within ~5 rows of the end. Keyed on the trailing visible index (not
  // the raw scroll position) so we fire at most once per row crossing.
  const virtualItems = virtualizer.getVirtualItems();
  const lastVisibleIndex = virtualItems.at(-1)?.index ?? -1;
  useEffect(() => {
    if (!hasNextPage || isFetchingNextPage) return;
    if (lastVisibleIndex < 0) return;
    if (lastVisibleIndex >= items.length - 5) {
      onLoadMore();
    }
  }, [lastVisibleIndex, items.length, hasNextPage, isFetchingNextPage, onLoadMore]);

  // Track the topmost visible item to drive the overlay sticky header.
  // We do this through a scroll listener (cheap; only reads scrollTop)
  // rather than a state-coupled hook so the overlay does not force the
  // virtualizer to re-render on every frame.
  useEffect(() => {
    const node = parentRef.current;
    if (!node) return;
    const update = (): void => {
      const top = node.scrollTop;
      // Find the last item whose start <= top — that is the chapter
      // currently anchoring the visible region.
      const virtualItems = virtualizer.getVirtualItems();
      let currentChapter: ChapterId | null = null;
      for (const vi of virtualItems) {
        if (vi.start > top) break;
        const it = items[vi.index];
        if (it) currentChapter = it.chapterId;
      }
      if (currentChapter !== null) {
        setStickyChapter((prev) => (prev === currentChapter ? prev : currentChapter));
      }
    };
    update();
    node.addEventListener('scroll', update, { passive: true });
    return () => {
      node.removeEventListener('scroll', update);
    };
  }, [items, virtualizer]);

  const setRowRef = useCallback(
    (id: string) =>
      (node: HTMLLIElement | null): void => {
        const map = rowRefs.current;
        if (node === null) {
          map.delete(id);
        } else {
          map.set(id, node);
        }
      },
    [],
  );

  const focusRow = useCallback(
    (id: string): void => {
      const node = rowRefs.current.get(id);
      if (node) {
        node.focus();
      } else {
        // The row is not mounted yet — scroll it into view, then the
        // virtualizer will mount it and our `onFocus` from the row
        // wires `focusedId`. We delay the focus call to next tick.
        const idx = items.findIndex((it) => it.kind === 'row' && it.task.id === id);
        if (idx >= 0) {
          virtualizer.scrollToIndex(idx, { align: 'auto' });
          requestAnimationFrame(() => {
            const after = rowRefs.current.get(id);
            if (after) after.focus();
          });
        }
      }
      setFocusedId(id);
    },
    [items, virtualizer],
  );

  const navigate = useCallback(
    (delta: 1 | -1): void => {
      if (orderedIds.length === 0) return;
      const currentIdx = focusedId ? orderedIds.indexOf(focusedId) : -1;
      const nextIdx =
        currentIdx === -1
          ? delta === 1
            ? 0
            : orderedIds.length - 1
          : Math.max(0, Math.min(orderedIds.length - 1, currentIdx + delta));
      const nextId = orderedIds[nextIdx];
      if (nextId) focusRow(nextId);
    },
    [focusedId, focusRow, orderedIds],
  );

  const handleListKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>): void => {
      if (event.key === 'j' || event.key === 'ArrowDown') {
        event.preventDefault();
        navigate(1);
        return;
      }
      if (event.key === 'k' || event.key === 'ArrowUp') {
        event.preventDefault();
        navigate(-1);
        return;
      }
      if (event.key === 'U' && event.shiftKey) {
        event.preventDefault();
        onUnarchiveSelected();
      }
    },
    [navigate, onUnarchiveSelected],
  );

  const handleRowFocus = useCallback((id: string): void => {
    setFocusedId(id);
  }, []);

  const stickyLabel = stickyChapter ? t(`chapter.${stickyChapter}`) : '';
  const stickyCount =
    stickyChapter !== null ? (groups.find((g) => g.id === stickyChapter)?.rows.length ?? 0) : 0;

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: scroll container coordinates roving keyboard navigation for its list rows; not an interactive control itself (role="presentation").
    <div
      ref={parentRef}
      className={styles.virtualScroll}
      onKeyDown={handleListKeyDown}
      role="presentation"
    >
      {stickyChapter !== null ? (
        <div className={styles.stickyChapter} aria-hidden="true">
          <span className={styles.chapterTitle}>{stickyLabel}</span>
          <span className={styles.chapterCount}>{stickyCount}</span>
        </div>
      ) : null}

      <ul className={styles.virtualInner} style={{ blockSize: `${virtualizer.getTotalSize()}px` }}>
        {virtualItems.map((vi) => {
          const item = items[vi.index];
          if (!item) return null;
          if (item.kind === 'chapter') {
            return (
              <li
                key={item.key}
                data-index={vi.index}
                ref={virtualizer.measureElement}
                className={styles.virtualRowWrap}
                style={{ transform: `translateY(${vi.start}px)` }}
              >
                <div className={styles.chapterHeader}>
                  <h2 className={styles.chapterTitle}>{t(`chapter.${item.chapterId}`)}</h2>
                  <span className={styles.chapterCount}>{item.count}</span>
                </div>
              </li>
            );
          }
          const archiverName =
            item.task.primaryAssigneeId !== null
              ? archiverNameById.get(item.task.primaryAssigneeId)
              : undefined;
          const archiverAvatarUrl =
            item.task.primaryAssigneeId !== null
              ? archiverAvatarById.get(item.task.primaryAssigneeId)
              : undefined;
          return (
            <li
              key={item.key}
              data-index={vi.index}
              ref={virtualizer.measureElement}
              className={styles.virtualRowWrap}
              style={{ transform: `translateY(${vi.start}px)` }}
            >
              <ArchivedRow
                task={item.task}
                selected={selected.has(item.task.id)}
                removing={removing.has(item.task.id)}
                focused={focusedId === item.task.id}
                locale={locale}
                {...(archiverName !== undefined ? { archiverName } : {})}
                {...(archiverAvatarUrl !== undefined ? { archiverAvatarUrl } : {})}
                onToggleSelect={onToggleSelect}
                onUnarchive={onUnarchive}
                onActivate={onActivate}
                onFocus={handleRowFocus}
                rowRef={setRowRef(item.task.id)}
              />
            </li>
          );
        })}
      </ul>

      {isFetchingNextPage ? (
        <div className={styles.loadingMore} aria-live="polite">
          {t('list.loadingMore')}
        </div>
      ) : null}
    </div>
  );
}
