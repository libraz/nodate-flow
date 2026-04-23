/**
 * PageList — main pages view with a two-pane layout.
 *
 * Left sidebar: PageTree component (recursive page tree).
 * Right content area: PageDetail or PageEditor depending on mode.
 * Search bar at the top of the tree sidebar. Empty state when no
 * workspace is selected.
 */

import { Link, useNavigate } from '@tanstack/react-router';
import { type ChangeEvent, type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Skeleton from '@nodate-flow/ui/primitives/skeleton';

import { useCurrentWorkspaceId } from '../../lib/use-current-workspace';
import { useWorkspacesQuery } from '../workspaces/api';
import { useSearchPages } from './api';
import PageDetail from './page-detail';
import PageEditor from './page-editor';
import PageTree from './page-tree';
import styles from './pages.module.css';

// ---------------------------------------------------------------------------
// Search results overlay
// ---------------------------------------------------------------------------

function SearchResults({
  workspaceId,
  query,
}: {
  workspaceId: string;
  query: string;
}): ReactElement | null {
  const { data: results, isLoading } = useSearchPages(workspaceId, query);

  if (query.length < 2) return null;
  if (isLoading) return <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />;
  if (!results || results.length === 0) return null;

  return (
    <ul className={styles.searchResults}>
      {results.map((page) => (
        <li key={page.id}>
          <Link
            to="/pages/$pageId"
            params={{ pageId: page.id }}
            className={styles.searchResultItem}
          >
            {page.title}
          </Link>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

type PageMode = 'view' | 'edit' | 'create';

interface PageListProps {
  /** Currently selected page id from the route, if any. */
  activePageId: string | undefined;
}

export default function PageList({ activePageId }: PageListProps): ReactElement {
  const { t } = useTranslation('pages');
  const navigate = useNavigate();
  const routeWsId = useCurrentWorkspaceId();
  const { data: workspaces } = useWorkspacesQuery();
  // Fall back to the first workspace on cross-workspace pages (e.g. /pages).
  const workspaceId = routeWsId ?? (workspaces.length === 1 ? (workspaces[0]?.id ?? null) : null);
  const [mode, setMode] = useState<PageMode>('view');
  const [searchQuery, setSearchQuery] = useState('');

  const handleSearchChange = (e: ChangeEvent<HTMLInputElement>): void => {
    setSearchQuery(e.target.value);
  };

  const handleCreatePage = (): void => {
    setMode('create');
  };

  const handleEdit = (): void => {
    setMode('edit');
  };

  const handleEditorDone = (savedPageId: string | undefined): void => {
    setMode('view');
    if (savedPageId) {
      void navigate({ to: '/pages/$pageId', params: { pageId: savedPageId } });
    }
  };

  if (!workspaceId) {
    return (
      <section className={styles.container}>
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>{t('empty')}</p>
          <p className={styles.emptyDescription}>{t('empty_description')}</p>
        </div>
      </section>
    );
  }

  return (
    <section className={styles.container}>
      <div className={styles.layout}>
        {/* Left sidebar: search + tree */}
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          <div className={styles.searchBar}>
            <input
              type="search"
              className={styles.searchInput}
              placeholder={t('search_placeholder')}
              value={searchQuery}
              onChange={handleSearchChange}
              aria-label={t('search_placeholder')}
            />
          </div>
          {searchQuery.length >= 2 ? (
            <div style={{ paddingInline: 'var(--nf-space-3)' }}>
              <SearchResults workspaceId={workspaceId} query={searchQuery} />
            </div>
          ) : (
            <Suspense
              fallback={
                <div style={{ padding: 'var(--nf-space-4)' }}>
                  <Skeleton style={{ blockSize: '1.5rem', inlineSize: '80%' }} />
                </div>
              }
            >
              <PageTree
                workspaceId={workspaceId}
                activePageId={activePageId}
                onCreatePage={handleCreatePage}
              />
            </Suspense>
          )}
        </div>

        {/* Right content area */}
        <div className={styles.contentArea}>
          {mode === 'create' && (
            <Suspense fallback={<Skeleton style={{ blockSize: '20rem', inlineSize: '100%' }} />}>
              <PageEditor
                workspaceId={workspaceId}
                existingPage={undefined}
                onDone={handleEditorDone}
              />
            </Suspense>
          )}
          {mode === 'edit' && activePageId && (
            <Suspense fallback={<Skeleton style={{ blockSize: '20rem', inlineSize: '100%' }} />}>
              <PageEditorWithData
                workspaceId={workspaceId}
                pageId={activePageId}
                onDone={handleEditorDone}
              />
            </Suspense>
          )}
          {mode === 'view' && activePageId && (
            <Suspense fallback={<Skeleton style={{ blockSize: '20rem', inlineSize: '100%' }} />}>
              <PageDetail workspaceId={workspaceId} pageId={activePageId} onEdit={handleEdit} />
            </Suspense>
          )}
          {mode === 'view' && !activePageId && (
            <div className={styles.empty}>
              <p className={styles.emptyTitle}>{t('no_selection')}</p>
              <p className={styles.emptyDescription}>{t('no_selection_description')}</p>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Editor wrapper that fetches page data first (suspends)
// ---------------------------------------------------------------------------

import { usePageQuery } from './api';

function PageEditorWithData({
  workspaceId,
  pageId,
  onDone,
}: {
  workspaceId: string;
  pageId: string;
  onDone: (savedPageId: string | undefined) => void;
}): ReactElement {
  const { data: page } = usePageQuery(workspaceId, pageId);
  return <PageEditor workspaceId={workspaceId} existingPage={page} onDone={onDone} />;
}
