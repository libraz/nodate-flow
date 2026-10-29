/**
 * PageTree — recursive tree component for the pages sidebar.
 *
 * Shows root pages with expand/collapse for children. Children are
 * lazy-loaded when a node is expanded (useChildPagesQuery). Active
 * page is highlighted. A "New page" button sits at the bottom.
 */

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Button from '@nodate-flow/ui/primitives/button';
import Collapse from '@nodate-flow/ui/primitives/collapse';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import PresenceItem from '@nodate-flow/ui/primitives/presence-item';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { Link } from '@tanstack/react-router';
import { ChevronRight, FileText, Plus } from 'lucide-react';
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
  const presence = usePresenceList(children, (child) => child.id);
  if (presence.entries.length === 0) return null;
  return (
    <ul className={styles.treeChildren}>
      {presence.entries.map((entry) => (
        <PresenceItem
          key={entry.key}
          as="li"
          present={entry.present}
          onExited={() => presence.onExited(entry.key)}
        >
          <TreeNode page={entry.item} workspaceId={workspaceId} activePageId={activePageId} />
        </PresenceItem>
      ))}
    </ul>
  );
}

function ChildTreeFallback(): ReactElement {
  return (
    <div className={styles.treeChildren} aria-hidden="true">
      <Skeleton className={styles.treeSkeletonRow} />
      <Skeleton className={styles.treeSkeletonRow} />
    </div>
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
          <ChevronRight size={14} aria-hidden className={styles.treeChevron} />
        </button>
        <FileText size={14} aria-hidden className={styles.treeIcon} />
        <Link
          to="/pages/$pageId"
          params={{ pageId: page.id }}
          className={styles.treeItemLabel}
          aria-current={isActive ? 'page' : undefined}
          title={page.title}
        >
          {page.title}
        </Link>
      </div>
      <Collapse open={expanded}>
        <Suspense fallback={<ChildTreeFallback />}>
          <ChildTreeContent
            parentId={page.id}
            workspaceId={workspaceId}
            activePageId={activePageId}
          />
        </Suspense>
      </Collapse>
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
  const presence = usePresenceList(pages, (page) => page.id);

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

      {presence.entries.length === 0 ? (
        <EmptyState titleAs="p" title={t('empty')} description={t('empty_description')} />
      ) : (
        <ul className={styles.treeList}>
          {presence.entries.map((entry) => (
            <PresenceItem
              key={entry.key}
              as="li"
              present={entry.present}
              onExited={() => presence.onExited(entry.key)}
            >
              <TreeNode page={entry.item} workspaceId={workspaceId} activePageId={activePageId} />
            </PresenceItem>
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
