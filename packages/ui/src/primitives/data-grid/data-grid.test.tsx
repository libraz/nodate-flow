import type { ColumnDef, ColumnPinningState, RowSelectionState } from '@tanstack/react-table';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactElement, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import DataGrid from './data-grid';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

interface Row {
  id: number;
  name: string;
  score: number;
}

const ROWS: Row[] = [
  { id: 1, name: 'Carol', score: 30 },
  { id: 2, name: 'Alice', score: 50 },
  { id: 3, name: 'Bob', score: 20 },
  { id: 4, name: 'Eve', score: 40 },
  { id: 5, name: 'Dave', score: 10 },
];

const COLS: ColumnDef<Row, unknown>[] = [
  { accessorKey: 'id', header: 'ID' },
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'score', header: 'Score' },
];

function renderGrid(props?: Partial<React.ComponentProps<typeof DataGrid<Row>>>): HTMLElement {
  const { container } = render(
    <DataGrid<Row> aria-label="people" columns={COLS} data={ROWS} {...props} />,
  );
  return container;
}

describe.each(THEMES)('DataGrid [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders 3 columns x 5 rows with no axe violations', async () => {
    const container = renderGrid();
    const grid = screen.getByRole('grid', { name: 'people' });
    expect(grid.getAttribute('aria-colcount')).toBe('3');
    expect(grid.getAttribute('aria-rowcount')).toBe('6');
    expect(screen.getAllByRole('columnheader')).toHaveLength(3);
    expect(screen.getAllByRole('gridcell')).toHaveLength(15);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('toggles aria-sort when a header is clicked', async () => {
    const user = userEvent.setup();
    renderGrid();
    const nameHeader = screen.getByRole('columnheader', { name: /Name/ });
    expect(nameHeader.getAttribute('aria-sort')).toBe('none');
    await user.click(nameHeader);
    expect(nameHeader.getAttribute('aria-sort')).toBe('ascending');
    await user.click(nameHeader);
    expect(nameHeader.getAttribute('aria-sort')).toBe('descending');
  });

  it('lets keyboard users focus a sortable header and toggle sorting', async () => {
    const user = userEvent.setup();
    renderGrid();
    const idHeader = screen.getByRole('columnheader', { name: /ID/ }) as HTMLElement;
    const nameHeader = screen.getByRole('columnheader', { name: /Name/ }) as HTMLElement;

    await user.tab();
    expect(document.activeElement).toBe(idHeader);

    await user.keyboard('{ArrowRight}');
    await waitFor(() => expect(document.activeElement).toBe(nameHeader));

    expect(nameHeader.getAttribute('aria-sort')).toBe('none');
    await user.keyboard('{Enter}');
    expect(nameHeader.getAttribute('aria-sort')).toBe('ascending');
  });

  it('moves focus with ArrowRight and ArrowDown (roving tabindex)', async () => {
    const user = userEvent.setup();
    renderGrid();
    const cells = screen.getAllByRole('gridcell');
    const first = cells[0] as HTMLElement;
    first.focus();
    await waitFor(() => expect(first.getAttribute('tabindex')).toBe('0'));

    await user.keyboard('{ArrowRight}');
    const right = document.querySelector<HTMLElement>('[data-row-index="0"][data-col-index="1"]');
    expect(right).not.toBeNull();
    expect(right?.getAttribute('tabindex')).toBe('0');

    await user.keyboard('{ArrowDown}');
    const down = document.querySelector<HTMLElement>('[data-row-index="1"][data-col-index="1"]');
    expect(down).not.toBeNull();
    expect(down?.getAttribute('tabindex')).toBe('0');
  });

  it('renders empty state when data is empty', () => {
    render(
      <DataGrid<Row>
        aria-label="empty"
        columns={COLS}
        data={[]}
        emptyContent={<span>nothing here</span>}
      />,
    );
    expect(screen.getByText('nothing here')).toBeDefined();
  });

  it('toggles row selection via checkbox column and fires onRowSelectionChange', () => {
    const onChange = vi.fn();

    function Wrapper(): ReactElement {
      const [sel, setSel] = useState<RowSelectionState>({});
      return (
        <DataGrid<Row>
          aria-label="selectable"
          columns={COLS}
          data={ROWS}
          enableRowSelection
          rowSelection={sel}
          onRowSelectionChange={(next) => {
            onChange(next);
            setSel(next);
          }}
        />
      );
    }

    render(<Wrapper />);
    // first body row, first cell holds the checkbox
    const firstBodyRow = screen.getAllByRole('row')[1] as HTMLElement;
    const checkbox = within(firstBodyRow).getByRole('checkbox') as HTMLInputElement;
    // happy-dom 20.x toggles `checked` on userEvent.click but does not dispatch
    // the `change` event, so React's onChange never runs. fireEvent.click
    // dispatches change synchronously and exercises the same code path.
    fireEvent.click(checkbox);
    expect(onChange).toHaveBeenCalled();
    expect(checkbox.checked).toBe(true);
  });

  it('keeps selection checkboxes out of the tab order and toggles from the roving cell', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();

    function Wrapper(): ReactElement {
      const [sel, setSel] = useState<RowSelectionState>({});
      return (
        <DataGrid<Row>
          aria-label="selectable"
          columns={COLS}
          data={ROWS}
          enableRowSelection
          rowSelection={sel}
          onRowSelectionChange={(next) => {
            onChange(next);
            setSel(next);
          }}
        />
      );
    }

    render(<Wrapper />);
    const firstBodyRow = screen.getAllByRole('row')[1] as HTMLElement;
    const selectionCell = within(firstBodyRow).getAllByRole('gridcell')[0] as HTMLElement;
    const checkbox = within(selectionCell).getByRole('checkbox') as HTMLInputElement;

    expect(checkbox.getAttribute('tabindex')).toBe('-1');
    selectionCell.focus();
    await waitFor(() => expect(selectionCell.getAttribute('tabindex')).toBe('0'));
    await user.keyboard(' ');

    expect(onChange).toHaveBeenCalled();
    await waitFor(() => {
      const updated = within(selectionCell).getByRole('checkbox') as HTMLInputElement;
      expect(updated.checked).toBe(true);
    });
  });

  it('keys row selection with getRowId when provided', () => {
    const onChange = vi.fn();

    function Wrapper(): ReactElement {
      const [sel, setSel] = useState<RowSelectionState>({});
      return (
        <DataGrid<Row>
          aria-label="selectable"
          columns={COLS}
          data={ROWS}
          getRowId={(row) => `row-${row.id}`}
          enableRowSelection
          rowSelection={sel}
          onRowSelectionChange={(next) => {
            onChange(next);
            setSel(next);
          }}
        />
      );
    }

    render(<Wrapper />);
    const firstBodyRow = screen.getAllByRole('row')[1] as HTMLElement;
    const checkbox = within(firstBodyRow).getByRole('checkbox') as HTMLInputElement;

    fireEvent.click(checkbox);

    expect(onChange).toHaveBeenLastCalledWith({ 'row-1': true });
  });

  it('renders resize handles when enableColumnResizing is true', async () => {
    const container = renderGrid({ enableColumnResizing: true });
    const separators = container.querySelectorAll('[role="separator"]');
    // Each of the 3 columns gets a resize handle
    expect(separators.length).toBe(3);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('does not render resize handles by default', () => {
    const container = renderGrid();
    const separators = container.querySelectorAll('[role="separator"]');
    expect(separators.length).toBe(0);
  });

  it('sets column size CSS variables when enableColumnResizing is true', () => {
    renderGrid({ enableColumnResizing: true });
    const grid = screen.getByRole('grid', { name: 'people' });
    const style = grid.getAttribute('style') ?? '';
    // CSS custom properties for each column should be present
    expect(style).toContain('--col-');
    expect(style).toContain('-size');
  });

  it('applies pinnedLeft class when enableColumnPinning and columnPinning are set', async () => {
    const pinning: ColumnPinningState = { left: ['id'] };
    const container = renderGrid({ enableColumnPinning: true, columnPinning: pinning });
    // The pinned header and body cells should have sticky positioning via inline style
    const grid = screen.getByRole('grid', { name: 'people' });
    const headers = grid.querySelectorAll('[role="columnheader"]');
    const firstHeader = headers[0] as HTMLElement;
    expect(firstHeader.style.position).toBe('sticky');
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe('DataGrid getRowProps', () => {
  it('attaches caller ref, handlers and data attributes to every body row', () => {
    const attached: HTMLElement[] = [];
    const onDrop = vi.fn();
    const container = renderGrid({
      getRowProps: (row) => ({
        ref: (node) => {
          if (node) attached.push(node);
        },
        'data-drop-index': row.index,
        className: 'caller-row',
        onDragOver: (e) => e.preventDefault(),
        onDrop: () => onDrop(row.index),
      }),
    });

    const rows = Array.from(
      container.querySelectorAll<HTMLElement>('[role="row"][data-drop-index]'),
    );
    expect(rows).toHaveLength(ROWS.length);
    expect(attached).toEqual(rows);
    expect(rows[2]?.getAttribute('data-drop-index')).toBe('2');
    expect(rows[2]?.className).toContain('caller-row');

    // A drop anywhere in the row — not just a handle cell — reaches the row.
    const nameCell = within(rows[2] as HTMLElement).getByText('Bob');
    fireEvent.drop(nameCell);
    expect(onDrop).toHaveBeenCalledWith(2);
  });

  it('keeps the grid-owned row semantics and position over caller props', () => {
    const container = renderGrid({
      enableRowSelection: true,
      getRowProps: () => ({
        style: { position: 'static', opacity: 0.5 },
        'aria-label': 'caller label',
      }),
    });
    const row = container.querySelector<HTMLElement>('[role="row"][aria-label="caller label"]');
    expect(row).not.toBeNull();
    expect(row?.getAttribute('role')).toBe('row');
    expect(row?.getAttribute('aria-selected')).toBe('false');
    expect(row?.style.position).toBe('absolute');
    expect(row?.style.opacity).toBe('0.5');
  });

  it('floors flex columns at an explicit minSize on header and body cells only', () => {
    const cols: ColumnDef<Row, unknown>[] = [
      { accessorKey: 'id', header: 'ID' },
      { accessorKey: 'name', header: 'Name', minSize: 120 },
    ];
    const container = renderGrid({ columns: cols });
    const q = (col: number, role: string) =>
      Array.from(
        container.querySelectorAll<HTMLElement>(`[role="${role}"][data-col-index="${col}"]`),
      );
    const named = [...q(1, 'columnheader'), ...q(1, 'gridcell')];
    expect(named.length).toBe(1 + ROWS.length);
    for (const el of named) expect(el.style.minInlineSize).toBe('120px');
    const plain = [...q(0, 'columnheader'), ...q(0, 'gridcell')];
    expect(plain.length).toBe(1 + ROWS.length);
    for (const el of plain) expect(el.style.minInlineSize).toBe('');
  });

  it('sets the table min-inline-size to the sum of column minSizes', () => {
    const cols: ColumnDef<Row, unknown>[] = [
      { accessorKey: 'id', header: 'ID' },
      { accessorKey: 'name', header: 'Name', minSize: 120 },
      { accessorKey: 'age', header: 'Age', minSize: 80 },
    ];
    const container = renderGrid({ columns: cols });
    const tableEl = container.querySelector<HTMLElement>('[role="grid"] > div');
    expect(tableEl?.style.minInlineSize).toBe('200px');
  });

  it('caps flex columns at an explicit maxSize on header and body cells only', () => {
    const cols: ColumnDef<Row, unknown>[] = [
      { accessorKey: 'id', header: 'ID', maxSize: 60 },
      { accessorKey: 'name', header: 'Name' },
    ];
    const container = renderGrid({ columns: cols });
    const q = (col: number, role: string) =>
      Array.from(
        container.querySelectorAll<HTMLElement>(`[role="${role}"][data-col-index="${col}"]`),
      );
    const capped = [...q(0, 'columnheader'), ...q(0, 'gridcell')];
    expect(capped.length).toBe(1 + ROWS.length);
    for (const el of capped) expect(el.style.maxInlineSize).toBe('60px');
    const plain = [...q(1, 'columnheader'), ...q(1, 'gridcell')];
    for (const el of plain) expect(el.style.maxInlineSize).toBe('');
  });

  it('keeps the row-selection column at a fixed width', () => {
    const container = renderGrid({ enableRowSelection: true });
    const cells = [
      ...container.querySelectorAll<HTMLElement>('[role="columnheader"][data-col-index="0"]'),
      ...container.querySelectorAll<HTMLElement>('[role="gridcell"][data-col-index="0"]'),
    ];
    expect(cells.length).toBe(1 + ROWS.length);
    for (const el of cells) {
      expect(el.style.minInlineSize).not.toBe('');
      expect(el.style.minInlineSize).toBe(el.style.maxInlineSize);
    }
  });
});
