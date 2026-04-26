/**
 * useArchiveFilters — local React state for archive filter pills.
 *
 * The Archive page is a single-screen surface so filter state is held in
 * `useState`, not Zustand. Exposing it through a dedicated hook keeps the
 * filter shape, defaults, and reset semantics in one place; component
 * code only sees `{ filters, setX, reset, isFiltered }` and never touches
 * the raw setter graph.
 *
 * Keys:
 *   - `search`        free-text query (matched client-side, ci substring)
 *   - `range`         '7d' | '30d' | '90d' | 'all' — relative archive window
 *   - `projectId`     restrict to one project ('' = any)
 *   - `archiverId`    restrict to one teammate ('' = any). Today this maps
 *                     to `primaryAssigneeId` because the backend list does
 *                     not currently surface the actual archiver. Documented
 *                     as a known gap; the field can swap to `archivedBy`
 *                     once the API ships it.
 */

import { useCallback, useMemo, useState } from 'react';

export type ArchiveRange = '7d' | '30d' | '90d' | 'all';

export interface ArchiveFiltersState {
  search: string;
  range: ArchiveRange;
  projectId: string;
  archiverId: string;
}

export const DEFAULT_ARCHIVE_FILTERS: ArchiveFiltersState = {
  search: '',
  range: 'all',
  projectId: '',
  archiverId: '',
};

export interface UseArchiveFiltersResult {
  filters: ArchiveFiltersState;
  setSearch: (next: string) => void;
  setRange: (next: ArchiveRange) => void;
  setProjectId: (next: string) => void;
  setArchiverId: (next: string) => void;
  reset: () => void;
  /** True when any filter departs from the defaults. */
  isFiltered: boolean;
}

export function useArchiveFilters(): UseArchiveFiltersResult {
  const [filters, setFilters] = useState<ArchiveFiltersState>(DEFAULT_ARCHIVE_FILTERS);

  const setSearch = useCallback((next: string) => {
    setFilters((prev) => ({ ...prev, search: next }));
  }, []);
  const setRange = useCallback((next: ArchiveRange) => {
    setFilters((prev) => ({ ...prev, range: next }));
  }, []);
  const setProjectId = useCallback((next: string) => {
    setFilters((prev) => ({ ...prev, projectId: next }));
  }, []);
  const setArchiverId = useCallback((next: string) => {
    setFilters((prev) => ({ ...prev, archiverId: next }));
  }, []);
  const reset = useCallback(() => {
    setFilters(DEFAULT_ARCHIVE_FILTERS);
  }, []);

  const isFiltered = useMemo(
    () =>
      filters.search.trim().length > 0 ||
      filters.range !== 'all' ||
      filters.projectId.length > 0 ||
      filters.archiverId.length > 0,
    [filters],
  );

  return { filters, setSearch, setRange, setProjectId, setArchiverId, reset, isFiltered };
}
