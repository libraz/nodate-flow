/**
 * DataGrid — virtualized, sortable, keyboard-navigable grid primitive.
 *
 * Built on `@tanstack/react-table` for column / sorting / selection state and
 * `@tanstack/react-virtual` for body row virtualization. Renders true ARIA grid
 * semantics with roving tabindex on `gridcell`s.
 *
 * Generic over `TData`. Pass column defs as `ColumnDef<TData>[]`.
 */

import {
  type ColumnDef,
  type ColumnPinningState,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  type OnChangeFn,
  type Row,
  type RowSelectionState,
  type SortingState,
  useReactTable,
} from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
  type CSSProperties,
  forwardRef,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  type Ref,
  useCallback,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './data-grid.module.css';

/**
 * Props for {@link DataGrid}.
 *
 * @typeParam TData - Row data shape.
 */
export interface DataGridProps<TData> {
  /** Column definitions (TanStack Table). */
  columns: ColumnDef<TData, unknown>[];
  /** Row data. */
  data: TData[];
  /** Stable row id factory used by TanStack state such as selection. */
  getRowId?: (originalRow: TData, index: number, parent?: Row<TData>) => string;
  /** Estimated row height in pixels for the virtualizer. Defaults to 36. */
  estimateSize?: number;
  /** Overscan rows above/below the viewport. Defaults to 5. */
  overscan?: number;
  /** Slot rendered when `data` is empty. */
  emptyContent?: ReactNode;
  /** Enables row selection via a leading checkbox column. */
  enableRowSelection?: boolean;
  /** Controlled row selection state. */
  rowSelection?: RowSelectionState;
  /** Called when row selection changes. */
  onRowSelectionChange?: (next: RowSelectionState) => void;
  /** Enables column resizing via drag handles on header cell edges. */
  enableColumnResizing?: boolean;
  /** Enables column pinning. Pinned-left columns stay fixed during horizontal scroll. */
  enableColumnPinning?: boolean;
  /** Controlled column pinning state. */
  columnPinning?: ColumnPinningState;
  /** Called when column pinning changes. */
  onColumnPinningChange?: (next: ColumnPinningState) => void;
  /** Optional accessible label. */
  'aria-label'?: string;
  /**
   * Accessible label for the header "select all rows" checkbox.
   * Defaults to `"Select all rows"` so consumers without i18n still get a
   * label; localised consumers should pass a translated string.
   */
  selectAllRowsLabel?: string;
  /**
   * Accessible label factory for per-row selection checkboxes.
   * Receives the 1-based row index. Defaults to `` `Select row ${index}` ``
   * so consumers without i18n still get a label; localised consumers should
   * pass a translated formatter (e.g. `t('tasks.list.select_row', { index })`).
   */
  selectRowLabel?: (index: number) => string;
  /** Optional class on the scroll container. */
  className?: string;
  /** Optional inline style on the scroll container. */
  style?: CSSProperties;
}

interface FocusedCell {
  row: number;
  col: number;
}

const PAGE_JUMP = 10;

function DataGridInner<TData>(
  props: DataGridProps<TData>,
  forwardedRef: Ref<HTMLDivElement>,
): ReactElement {
  const {
    columns,
    data,
    getRowId,
    estimateSize = 36,
    overscan = 5,
    emptyContent,
    enableRowSelection = false,
    rowSelection,
    onRowSelectionChange,
    enableColumnResizing = false,
    enableColumnPinning = false,
    columnPinning: controlledPinning,
    onColumnPinningChange,
    selectAllRowsLabel = 'Select all rows',
    selectRowLabel = (index: number) => `Select row ${index}`,
    className,
    style,
  } = props;
  const ariaLabel = props['aria-label'];

  const scrollRef = useRef<HTMLDivElement | null>(null);
  useImperativeHandle(forwardedRef, () => scrollRef.current as HTMLDivElement);

  const [sorting, setSorting] = useState<SortingState>([]);
  const [internalSelection, setInternalSelection] = useState<RowSelectionState>({});
  const selection = rowSelection ?? internalSelection;
  const [internalPinning, setInternalPinning] = useState<ColumnPinningState>({});
  const pinning = controlledPinning ?? internalPinning;

  const handlePinningChange: OnChangeFn<ColumnPinningState> = useCallback(
    (updater) => {
      const next = typeof updater === 'function' ? updater(pinning) : updater;
      if (controlledPinning === undefined) setInternalPinning(next);
      onColumnPinningChange?.(next);
    },
    [pinning, controlledPinning, onColumnPinningChange],
  );

  const handleSelectionChange: OnChangeFn<RowSelectionState> = useCallback(
    (updater) => {
      const next = typeof updater === 'function' ? updater(selection) : updater;
      if (rowSelection === undefined) setInternalSelection(next);
      onRowSelectionChange?.(next);
    },
    [selection, rowSelection, onRowSelectionChange],
  );

  // Optionally prepend a selection checkbox column.
  const finalColumns = useMemo<ColumnDef<TData, unknown>[]>(() => {
    if (!enableRowSelection) return columns;
    const selectCol: ColumnDef<TData, unknown> = {
      id: '__select',
      header: ({ table }) => (
        <input
          type="checkbox"
          aria-label={selectAllRowsLabel}
          checked={table.getIsAllRowsSelected()}
          ref={(el) => {
            if (el) el.indeterminate = table.getIsSomeRowsSelected();
          }}
          onChange={table.getToggleAllRowsSelectedHandler()}
        />
      ),
      cell: ({ row }) => (
        <input
          type="checkbox"
          aria-label={selectRowLabel(row.index + 1)}
          checked={row.getIsSelected()}
          onChange={row.getToggleSelectedHandler()}
        />
      ),
      enableSorting: false,
    };
    return [selectCol, ...columns];
  }, [columns, enableRowSelection, selectAllRowsLabel, selectRowLabel]);

  const table = useReactTable<TData>({
    data,
    columns: finalColumns,
    state: { sorting, rowSelection: selection, columnPinning: pinning },
    onSortingChange: setSorting,
    onRowSelectionChange: handleSelectionChange,
    onColumnPinningChange: handlePinningChange,
    enableRowSelection,
    enableColumnResizing,
    columnResizeMode: 'onChange',
    enableColumnPinning,
    ...(getRowId ? { getRowId } : {}),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  const rows = table.getRowModel().rows;
  const colCount = table.getVisibleLeafColumns().length;

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => estimateSize,
    overscan,
  });

  const virtualRows = virtualizer.getVirtualItems();
  // happy-dom has no layout, so virtualizer can return 0 items. Fall back to
  // rendering all rows when nothing is measured yet so tests stay deterministic.
  const useFallback = virtualRows.length === 0 && rows.length > 0;
  const totalSize = useFallback ? rows.length * estimateSize : virtualizer.getTotalSize();

  const [focused, setFocused] = useState<FocusedCell>({ row: 0, col: 0 });

  const moveFocus = useCallback(
    (nextRow: number, nextCol: number) => {
      const r = Math.max(0, Math.min(rows.length - 1, nextRow));
      const c = Math.max(0, Math.min(colCount - 1, nextCol));
      setFocused({ row: r, col: c });
      requestAnimationFrame(() => {
        const el = scrollRef.current?.querySelector<HTMLElement>(
          `[data-row-index="${r}"][data-col-index="${c}"]`,
        );
        el?.focus();
      });
    },
    [rows.length, colCount],
  );

  const onCellKeyDown = useCallback(
    (e: KeyboardEvent<HTMLDivElement>, rowIdx: number, colIdx: number) => {
      switch (e.key) {
        case 'ArrowRight':
          e.preventDefault();
          moveFocus(rowIdx, colIdx + 1);
          break;
        case 'ArrowLeft':
          e.preventDefault();
          moveFocus(rowIdx, colIdx - 1);
          break;
        case 'ArrowDown':
          e.preventDefault();
          moveFocus(rowIdx + 1, colIdx);
          break;
        case 'ArrowUp':
          e.preventDefault();
          moveFocus(rowIdx - 1, colIdx);
          break;
        case 'Home':
          e.preventDefault();
          moveFocus(e.ctrlKey ? 0 : rowIdx, 0);
          break;
        case 'End':
          e.preventDefault();
          moveFocus(e.ctrlKey ? rows.length - 1 : rowIdx, colCount - 1);
          break;
        case 'PageDown':
          e.preventDefault();
          moveFocus(rowIdx + PAGE_JUMP, colIdx);
          break;
        case 'PageUp':
          e.preventDefault();
          moveFocus(rowIdx - PAGE_JUMP, colIdx);
          break;
        default:
          break;
      }
    },
    [moveFocus, rows.length, colCount],
  );

  const headerGroups = table.getHeaderGroups();

  /**
   * When resizing is active, derive column widths from the table sizing state
   * so the grid updates on every drag frame (`columnResizeMode: 'onChange'`).
   */
  const columnSizeVars: Record<string, string> | undefined = enableColumnResizing
    ? (() => {
        const headers = table.getFlatHeaders();
        const vars: Record<string, string> = {};
        for (const header of headers) {
          vars[`--col-${header.id}-size`] = `${header.getSize()}px`;
        }
        return vars;
      })()
    : undefined;

  /** Compute sticky inline-start offset for pinned-left headers / cells. */
  const getPinnedOffset = (colIndex: number, headers: { getSize: () => number }[]): number => {
    let offset = 0;
    for (let i = 0; i < colIndex; i++) {
      const h = headers[i];
      if (h) offset += h.getSize();
    }
    return offset;
  };

  const renderRows: { row: (typeof rows)[number] | undefined; idx: number; start: number }[] =
    useFallback
      ? rows.map((row, idx) => ({ row, idx, start: idx * estimateSize }))
      : virtualRows.map((vr) => ({ row: rows[vr.index], idx: vr.index, start: vr.start }));

  return (
    <div
      ref={scrollRef}
      role="grid"
      aria-label={ariaLabel}
      aria-rowcount={rows.length + headerGroups.length}
      aria-colcount={colCount}
      className={cx(styles.root, className)}
      style={{ ...columnSizeVars, ...style } as CSSProperties}
      tabIndex={-1}
    >
      <div className={styles.table}>
        {headerGroups.map((group, gIdx) => (
          <div key={group.id} role="row" aria-rowindex={gIdx + 1} className={styles.headerRow}>
            {group.headers.map((header, hIdx) => {
              const canSort = header.column.getCanSort();
              const sortDir = header.column.getIsSorted();
              const ariaSort: 'ascending' | 'descending' | 'none' | undefined = canSort
                ? sortDir === 'asc'
                  ? 'ascending'
                  : sortDir === 'desc'
                    ? 'descending'
                    : 'none'
                : undefined;
              const isPinned = header.column.getIsPinned();
              const headerStyle: CSSProperties = enableColumnResizing
                ? { inlineSize: `var(--col-${header.id}-size)`, flex: 'none' }
                : { flex: `${header.getSize()} 1 0` };
              if (isPinned === 'left') {
                headerStyle.position = 'sticky';
                headerStyle.insetInlineStart = `${getPinnedOffset(hIdx, group.headers)}px`;
                headerStyle.zIndex = 2;
              }
              return (
                <div
                  key={header.id}
                  role="columnheader"
                  aria-colindex={hIdx + 1}
                  aria-sort={ariaSort}
                  tabIndex={-1}
                  className={cx(styles.headerCell, isPinned === 'left' && styles.pinnedLeft)}
                  style={headerStyle}
                  onClick={canSort ? header.column.getToggleSortingHandler() : undefined}
                  onKeyDown={(e) => {
                    if (canSort && (e.key === 'Enter' || e.key === ' ')) {
                      e.preventDefault();
                      header.column.toggleSorting();
                    }
                  }}
                >
                  {header.isPlaceholder
                    ? null
                    : flexRender(header.column.columnDef.header, header.getContext())}
                  {sortDir === 'asc' && <span className={styles.sortIndicator}>▲</span>}
                  {sortDir === 'desc' && <span className={styles.sortIndicator}>▼</span>}
                  {enableColumnResizing && header.column.getCanResize() && (
                    <div
                      className={cx(
                        styles.resizeHandle,
                        header.column.getIsResizing() && styles.resizing,
                      )}
                      onMouseDown={header.getResizeHandler()}
                      onTouchStart={header.getResizeHandler()}
                      onDoubleClick={() => header.column.resetSize()}
                      // biome-ignore lint/a11y/useAriaPropsForRole: pointer-only column-resize affordance; exposed as a vertical separator without value attributes since it is not a focusable window-splitter widget
                      role="separator"
                      aria-orientation="vertical"
                    />
                  )}
                </div>
              );
            })}
          </div>
        ))}

        {rows.length === 0 ? (
          <div role="row" aria-rowindex={headerGroups.length + 1}>
            <div role="gridcell" aria-colindex={1} className={styles.empty}>
              {emptyContent ?? 'No data'}
            </div>
          </div>
        ) : (
          <div
            className={styles.body}
            style={{ blockSize: `${totalSize}px`, position: 'relative' }}
          >
            {renderRows.map(({ row, idx, start }) => {
              if (!row) return null;
              const visibleCells = row.getVisibleCells();
              return (
                <div
                  key={row.id}
                  role="row"
                  aria-rowindex={headerGroups.length + idx + 1}
                  aria-selected={enableRowSelection ? row.getIsSelected() : undefined}
                  className={styles.row}
                  style={{
                    position: 'absolute',
                    insetInlineStart: 0,
                    insetInlineEnd: 0,
                    insetBlockStart: `${start}px`,
                  }}
                >
                  {visibleCells.map((cell, cIdx) => {
                    const isFocused = focused.row === idx && focused.col === cIdx;
                    const isPinned = cell.column.getIsPinned();
                    const cellStyle: CSSProperties = enableColumnResizing
                      ? { inlineSize: `var(--col-${cell.column.id}-size)`, flex: 'none' }
                      : { flex: `${cell.column.getSize()} 1 0` };
                    if (isPinned === 'left') {
                      cellStyle.position = 'sticky';
                      cellStyle.insetInlineStart = `${getPinnedOffset(
                        cIdx,
                        row.getVisibleCells().map((c) => c.column),
                      )}px`;
                      cellStyle.zIndex = 1;
                    }
                    return (
                      <div
                        key={cell.id}
                        role="gridcell"
                        aria-colindex={cIdx + 1}
                        data-row-index={idx}
                        data-col-index={cIdx}
                        tabIndex={isFocused ? 0 : -1}
                        className={cx(styles.cell, isPinned === 'left' && styles.pinnedLeft)}
                        style={cellStyle}
                        onFocus={() => setFocused({ row: idx, col: cIdx })}
                        onKeyDown={(e) => onCellKeyDown(e, idx, cIdx)}
                      >
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </div>
                    );
                  })}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * Generic virtualized data grid primitive. See {@link DataGridProps}.
 */
const DataGrid = forwardRef(DataGridInner) as <TData>(
  props: DataGridProps<TData> & { ref?: Ref<HTMLDivElement> },
) => ReactElement;

export default DataGrid;
