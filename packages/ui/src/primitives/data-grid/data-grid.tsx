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
  type OnChangeFn,
  type RowSelectionState,
  type SortingState,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  type Ref,
  forwardRef,
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
  /** Optional accessible label. */
  'aria-label'?: string;
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
    estimateSize = 36,
    overscan = 5,
    emptyContent,
    enableRowSelection = false,
    rowSelection,
    onRowSelectionChange,
    className,
    style,
  } = props;
  const ariaLabel = props['aria-label'];

  const scrollRef = useRef<HTMLDivElement | null>(null);
  useImperativeHandle(forwardedRef, () => scrollRef.current as HTMLDivElement);

  const [sorting, setSorting] = useState<SortingState>([]);
  const [internalSelection, setInternalSelection] = useState<RowSelectionState>({});
  const selection = rowSelection ?? internalSelection;

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
          aria-label="Select all rows"
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
          aria-label={`Select row ${row.index + 1}`}
          checked={row.getIsSelected()}
          onChange={row.getToggleSelectedHandler()}
        />
      ),
      enableSorting: false,
    };
    return [selectCol, ...columns];
  }, [columns, enableRowSelection]);

  const table = useReactTable<TData>({
    data,
    columns: finalColumns,
    state: { sorting, rowSelection: selection },
    onSortingChange: setSorting,
    onRowSelectionChange: handleSelectionChange,
    enableRowSelection,
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
      style={style}
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
              return (
                <div
                  key={header.id}
                  role="columnheader"
                  aria-colindex={hIdx + 1}
                  aria-sort={ariaSort}
                  tabIndex={-1}
                  className={styles.headerCell}
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
                    return (
                      <div
                        key={cell.id}
                        role="gridcell"
                        aria-colindex={cIdx + 1}
                        data-row-index={idx}
                        data-col-index={cIdx}
                        tabIndex={isFocused ? 0 : -1}
                        className={styles.cell}
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
