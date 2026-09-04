/**
 * PageDetail — read-only view of a single wiki page.
 *
 * Renders title, creator, updated date, Markdown body, breadcrumb
 * (parent chain), and a sub-pages section when children exist.
 * Provides Edit and Delete action buttons.
 */

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbSeparator,
} from '@nodate-flow/ui/primitives/breadcrumb';
import Button from '@nodate-flow/ui/primitives/button';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { Link, useNavigate } from '@tanstack/react-router';
import { FileText, Pencil, Sparkles, Trash2 } from 'lucide-react';
import { type ReactElement, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { formatApiError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import { type PageItem, useChildPagesQuery, useDeletePage, usePageQuery } from './api';
import styles from './pages.module.css';

// ---------------------------------------------------------------------------
// Children section (suspends)
// ---------------------------------------------------------------------------

function ChildPagesSection({
  workspaceId,
  pageId,
}: {
  workspaceId: string;
  pageId: string;
}): ReactElement | null {
  const { t } = useTranslation('pages');
  const { data: children } = useChildPagesQuery(workspaceId, pageId);

  if (children.length === 0) return null;

  return (
    <section className={styles.childrenSection}>
      <h3 className={styles.childrenTitle}>{t('children')}</h3>
      <ul className={styles.childrenList}>
        {children.map((child) => (
          <li key={child.id}>
            <Link to="/pages/$pageId" params={{ pageId: child.id }} className={styles.childLink}>
              <FileText size={14} aria-hidden />
              {child.title}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Breadcrumb
// ---------------------------------------------------------------------------

function PageBreadcrumb({ page }: { page: PageItem }): ReactElement | null {
  const { t } = useTranslation('pages');
  if (!page.parentPageId || !page.parentPageTitle) return null;

  return (
    <Breadcrumb label={t('breadcrumb_label')}>
      <BreadcrumbItem asChild>
        <Link to="/pages/$pageId" params={{ pageId: page.parentPageId }}>
          {page.parentPageTitle}
        </Link>
      </BreadcrumbItem>
      <BreadcrumbSeparator />
      <BreadcrumbItem>{page.title}</BreadcrumbItem>
    </Breadcrumb>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

interface PageDetailProps {
  workspaceId: string;
  pageId: string;
  onEdit: () => void;
}

export default function PageDetail({ workspaceId, pageId, onEdit }: PageDetailProps): ReactElement {
  const { t, i18n } = useTranslation('pages');
  const locale = i18n.resolvedLanguage ?? 'en';
  const navigate = useNavigate();
  const { data: page } = usePageQuery(workspaceId, pageId);
  const deleteMutation = useDeletePage(workspaceId);

  const handleDelete = async (): Promise<void> => {
    const confirmed = await confirmAction({
      message: t('confirm_delete'),
      tone: 'danger',
    });
    if (!confirmed) return;
    // Navigating first would report a delete the API may still refuse, leaving
    // the page in the list behind a screen that says it is gone.
    try {
      await deleteMutation.mutateAsync(pageId);
    } catch (err) {
      toaster.show({ tone: 'danger', message: formatApiError(err, t, 'errors.delete_failed') });
      return;
    }
    void navigate({ to: '/pages' });
  };

  const updatedLabel = t('updated_at', {
    date: new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(page.updatedAt * 1000)),
  });

  const createdByLabel = t('created_by', { name: page.creatorDisplayName });

  return (
    <article className={styles.detailContainer}>
      <PageBreadcrumb page={page} />

      <header className={styles.detailHeader}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between' }}>
          <h1 className={styles.detailTitle}>{page.title}</h1>
          <div className={styles.detailActions}>
            <Button type="button" variant="ghost" size="sm" onClick={onEdit}>
              <Pencil size={14} aria-hidden />
              {t('edit')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void handleDelete()}
              disabled={deleteMutation.isPending}
            >
              <Trash2 size={14} aria-hidden />
              {t('delete')}
            </Button>
          </div>
        </div>
        <div className={styles.detailMeta}>
          <span>{createdByLabel}</span>
          <span>{updatedLabel}</span>
          {page.isAiGenerated && (
            <span className={styles.aiBadge}>
              <Sparkles size={12} aria-hidden style={{ marginInlineEnd: 'var(--nf-space-1)' }} />
              AI
            </span>
          )}
          {page.projectName && <span>{page.projectName}</span>}
        </div>
      </header>

      {page.body && page.body.trim().length > 0 ? (
        <div className={styles.detailBody}>
          <Markdown>{page.body}</Markdown>
        </div>
      ) : null}

      <Suspense fallback={null}>
        <ChildPagesSection workspaceId={workspaceId} pageId={pageId} />
      </Suspense>
    </article>
  );
}
