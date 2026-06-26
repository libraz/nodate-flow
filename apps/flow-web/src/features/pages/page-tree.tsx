/**
 * PageTree — recursive tree component for the pages sidebar.
 *
 * Shows root pages with expand/collapse for children. Children are
 * lazy-loaded when a node is expanded (useChildPagesQuery). Active
 * page is highlighted. A "New page" button sits at the bottom.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { Link } from '@tanstack/react-router';
import { ChevronDown, ChevronRight, FileText, Plus } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type PageItem, useChildPagesQuery, usePagesQuery } from './api';
import styles from './pages.module.css';

// ---------------------------------------------------------------------------
// Child tree node (suspends on child page fetch)
// ---------------------------------------------------------------------------

interface ChildTreeNodeProps {
  page: PageItem;
  workspaceId: string;
  activePageId: string | undefined;
}

function ChildTreeContent({
  parentId,
  workspaceId,
  activePageId,
}: {
  parentId: string;
  workspaceId: string;
  activePageId: string | undefined;
}): ReactElement | null {
  const { data: children } = useChildPagesQuery(workspaceId, parentId);
  if (children.length === 0) return null;
  return (
    <ul className={styles.treeChildren}>
      {children.map((child) => (
        <li key={child.id}>
          <TreeNode page={child} workspaceId={workspaceId} activePageId={activePageId} />
        </li>
      ))}
    </ul>
  );
}

function TreeNode({ page, workspaceId, activePageId }: ChildTreeNodeProps): ReactElement {
  const { t } = useTranslation('pages');
  const [expanded, setExpanded] = useState(false);
  const isActive = page.id === activePageId;

  const handleToggle = (): void => {
    setExpanded((prev) => !prev);
  };

  return (
    <>
      <div className={`${styles.treeItem ?? ''} ${isActive ? (styles.treeItemActive ?? '') : ''}`}>
        <button
          type="button"
          className={styles.treeToggle}
          onClick={handleToggle}
          aria-label={expanded ? t('tree.collapse') : t('tree.expand')}
          aria-expanded={expanded}
        >
          {expanded ? (
            <ChevronDown size={14} aria-hidden />
          ) : (
            <ChevronRight size={14} aria-hidden />
          )}
        </button>
        <FileText size={14} aria-hidden style={{ flexShrink: 0 }} />
        <Link
          to="/pages/$pageId"
          params={{ pageId: page.id }}
          className={styles.treeItemLabel}
          style={{ color: 'inherit', textDecoration: 'none' }}
        >
          {page.title}
        </Link>
      </div>
      {expanded && (
        <Suspense fallback={null}>
          <ChildTreeContent
            parentId={page.id}
            workspaceId={workspaceId}
            activePageId={activePageId}
          />
        </Suspense>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Root tree
// ---------------------------------------------------------------------------

interface PageTreeProps {
  workspaceId: string;
  activePageId: string | undefined;
  onCreatePage: () => void;
}

export default function PageTree({
  workspaceId,
  activePageId,
  onCreatePage,
}: PageTreeProps): ReactElement {
  const { t } = useTranslation('pages');
  const { data: pages } = usePagesQuery(workspaceId);

  return (
    <div className={styles.treeSidebar}>
      <div className={styles.treeHeader}>
        <h2 className={styles.treeTitle}>{t('tree.root')}</h2>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCreatePage}
          aria-label={t('create')}
        >
          <Plus size={16} aria-hidden />
        </Button>
      </div>

      {pages.length === 0 ? (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>{t('empty')}</p>
          <p className={styles.emptyDescription}>{t('empty_description')}</p>
        </div>
      ) : (
        <ul className={styles.treeList}>
          {pages.map((page) => (
            <li key={page.id}>
              <TreeNode page={page} workspaceId={workspaceId} activePageId={activePageId} />
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="default" size="sm" onClick={onCreatePage}>
        <Plus size={14} aria-hidden />
        {t('create')}
      </Button>
    </div>
  );
}
