/**
 * ArchivedFilters — sticky filter bar for the Archive page.
 *
 * Exposes:
 *   - debounced search input (200ms)
 *   - 7d / 30d / 90d / All range pills (radiogroup)
 *   - Project picker (any-project sentinel)
 *   - Archiver picker (any-teammate sentinel)
 *
 * Selectors are native `<select>` styled by the design system so the
 * page works without a heavy combobox dependency. The range pills are
 * an explicit `role="radiogroup"` so screen readers announce the
 * selection pattern correctly without relying on visual styling.
 *
 * Search debouncing avoids re-running the chapter classifier on every
 * keystroke. The local `text` value tracks the input verbatim while the
 * deferred update flows back into the parent filter store.
 */

import Input from '@nodate-flow/ui/primitives/input';
import Select from '@nodate-flow/ui/primitives/select';
import { type ChangeEvent, type ReactElement, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { Project } from '../../projects/api';
import type { WorkspaceMember } from '../../workspaces/api';

import styles from './archived.module.css';
import type { ArchiveRange } from './hooks/use-archive-filters';

const RANGES: readonly ArchiveRange[] = ['7d', '30d', '90d', 'all'] as const;
const SEARCH_DEBOUNCE_MS = 200;

export interface ArchivedFiltersProps {
  search: string;
  range: ArchiveRange;
  projectId: string;
  archiverId: string;
  projects: readonly Project[];
  members: readonly WorkspaceMember[];
  onSearchChange: (next: string) => void;
  onRangeChange: (next: ArchiveRange) => void;
  onProjectChange: (next: string) => void;
  onArchiverChange: (next: string) => void;
}

export default function ArchivedFilters({
  search,
  range,
  projectId,
  archiverId,
  projects,
  members,
  onSearchChange,
  onRangeChange,
  onProjectChange,
  onArchiverChange,
}: ArchivedFiltersProps): ReactElement {
  const { t } = useTranslation('archive');
  const [text, setText] = useState(search);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Reflect external resets (e.g. "Clear filters" CTA) back into the
  // input — without this the field would keep showing stale text after
  // the parent zeroed the filter store.
  useEffect(() => {
    setText(search);
  }, [search]);

  useEffect(() => {
    return () => {
      if (timer.current !== null) clearTimeout(timer.current);
    };
  }, []);

  const handleSearch = (event: ChangeEvent<HTMLInputElement>): void => {
    const next = event.target.value;
    setText(next);
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      onSearchChange(next);
    }, SEARCH_DEBOUNCE_MS);
  };

  return (
    <div className={styles.filterBar} role="search" aria-label={t('page.title')}>
      <Input
        type="search"
        className={styles.filterSearch}
        placeholder={t('filters.searchPlaceholder')}
        aria-label={t('filters.searchPlaceholder')}
        value={text}
        onChange={handleSearch}
      />

      <div role="radiogroup" aria-label={t('filters.range.all')} className={styles.rangeGroup}>
        {RANGES.map((value) => (
          <button
            key={value}
            type="button"
            role="radio"
            aria-checked={range === value}
            className={styles.rangePill}
            onClick={() => {
              onRangeChange(value);
            }}
          >
            {t(`filters.range.${value}`)}
          </button>
        ))}
      </div>

      <div className={styles.filterGroup}>
        <label htmlFor="archive-project-filter">{t('filters.project')}</label>
        <Select
          id="archive-project-filter"
          value={projectId}
          onChange={(e) => {
            onProjectChange(e.target.value);
          }}
        >
          <option value="">{t('filters.anyProject')}</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
      </div>

      <div className={styles.filterGroup}>
        <label htmlFor="archive-archiver-filter">{t('filters.archiver')}</label>
        <Select
          id="archive-archiver-filter"
          value={archiverId}
          onChange={(e) => {
            onArchiverChange(e.target.value);
          }}
        >
          <option value="">{t('filters.anyArchiver')}</option>
          {members.map((m) => (
            <option key={m.id} value={m.userId}>
              {m.displayName}
            </option>
          ))}
        </Select>
      </div>
    </div>
  );
}
