import type { ColumnDef, ColumnPinningState, RowSelectionState } from '@tanstack/react-table';
import { fireEvent, render, screen, within } from '@testing-library/react';
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

  it('moves focus with ArrowRight and ArrowDown (roving tabindex)', async () => {
    const user = userEvent.setup();
    renderGrid();
    const cells = screen.getAllByRole('gridcell');
    const first = cells[0] as HTMLElement;
    first.focus();
    expect(first.getAttribute('tabindex')).toBe('0');

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
