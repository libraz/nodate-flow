/**
 * TableRegion — the loading / empty / populated frame around an admin table.
 *
 * The first load shows skeleton rows; a refetch keeps the previous rows on
 * screen under `aria-busy` instead of blanking them, so paging or typing
 * into a filter does not collapse the page between responses.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import type { ReactElement, ReactNode } from 'react';

const SKELETON_ROWS = ['a', 'b', 'c', 'd', 'e'] as const;

export interface TableRegionProps {
  /** A request for the rows is in flight. */
  loading: boolean;
  /** Number of rows currently held. */
  rowCount: number;
  /** Already-translated "loading" label, announced while skeletons show. */
  loadingLabel: string;
  /** Already-translated title shown when the list is empty. */
  emptyTitle: string;
  /** The `<table>`, rendered inside the horizontal scroll container. */
  children: ReactNode;
}

export default function TableRegion({
  loading,
  rowCount,
  loadingLabel,
  emptyTitle,
  children,
}: TableRegionProps): ReactElement {
  if (rowCount === 0 && loading) {
    return (
      <div className="aw-table-skeleton" aria-busy="true">
        <VisuallyHidden>{loadingLabel}</VisuallyHidden>
        {SKELETON_ROWS.map((id) => (
          <Skeleton key={id} />
        ))}
      </div>
    );
  }
  if (rowCount === 0) {
    return <EmptyState title={emptyTitle} titleAs="p" />;
  }
  return (
    <div className="aw-table-scroll" aria-busy={loading || undefined}>
      {children}
    </div>
  );
}
