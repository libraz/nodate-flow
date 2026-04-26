/**
 * DescriptionHistoryDrawer — right-rail drawer that lists prior task
 * description versions, previews the selected version with a line-level
 * diff against the current description, and lets the actor restore.
 *
 * Mounted by the task detail route via a "History" trigger next to the
 * description card. The drawer fetches the version list lazily via
 * Suspense; a nested Suspense boundary loads the selected version's
 * full body. Restoring invalidates the description history list and
 * the task detail row so the new active description shows up
 * immediately, then closes the drawer.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError } from '../../../lib/api-error';
import { formatDateTime } from '../../../lib/format';
import {
  type DescriptionVersion,
  useDescriptionHistoryQuery,
  useDescriptionVersionQuery,
  useRestoreDescriptionVersion,
} from '../description-history-api';
import styles from './description-history-drawer.module.css';
import { type DiffLine, diffLines } from './diff';

export interface DescriptionHistoryDrawerProps {
  taskId: string;
  /** Current description body, used as the right side of the diff. */
  currentBody: string;
  open: boolean;
  onClose: () => void;
}

export default function DescriptionHistoryDrawer({
  taskId,
  currentBody,
  open,
  onClose,
}: DescriptionHistoryDrawerProps): ReactElement {
  const { t } = useTranslation('common');

  return (
    <Drawer open={open} onClose={onClose} title={t('tasks.history.title')} side="inline-end">
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
          </div>
        }
      >
        <HistoryBody taskId={taskId} currentBody={currentBody} onAfterRestore={onClose} />
      </Suspense>
    </Drawer>
  );
}

interface HistoryBodyProps {
  taskId: string;
  currentBody: string;
  onAfterRestore: () => void;
}

function HistoryBody({ taskId, currentBody, onAfterRestore }: HistoryBodyProps): ReactElement {
  const { t, i18n } = useTranslation('common');
  const { data: versions } = useDescriptionHistoryQuery(taskId);
  const restore = useRestoreDescriptionVersion();
  const locale = i18n.resolvedLanguage ?? 'en';

  const [selectedId, setSelectedId] = useState<string>(versions[0]?.id ?? '');

  if (versions.length === 0) {
    return <p className={styles.empty}>{t('tasks.history.empty')}</p>;
  }

  const handleRestore = (versionId: string): void => {
    restore.mutate(
      { taskId, versionId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('tasks.history.restore_success') });
          onAfterRestore();
        },
        onError: (err) => {
          const message = err instanceof ApiError ? err.message : t('tasks.history.restore_error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  return (
    <div className={styles.body}>
      <ul className={styles.versionList}>
        {versions.map((version) => (
          <li key={version.id}>
            <VersionEntry
              version={version}
              active={version.id === selectedId}
              locale={locale}
              onSelect={() => setSelectedId(version.id)}
            />
          </li>
        ))}
      </ul>
      <div className={styles.preview}>
        {selectedId ? (
          <Suspense
            fallback={
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
                <Skeleton style={{ blockSize: '1.5rem', inlineSize: '50%' }} />
                <Skeleton style={{ blockSize: '8rem', inlineSize: '100%' }} />
              </div>
            }
          >
            <VersionPreview
              taskId={taskId}
              versionId={selectedId}
              currentBody={currentBody}
              onRestore={handleRestore}
              restorePending={restore.isPending}
            />
          </Suspense>
        ) : null}
      </div>
    </div>
  );
}

interface VersionEntryProps {
  version: DescriptionVersion;
  active: boolean;
  locale: string;
  onSelect: () => void;
}

function VersionEntry({ version, active, locale, onSelect }: VersionEntryProps): ReactElement {
  const { t } = useTranslation('common');
  const className = active
    ? `${styles.versionItem} ${styles.versionItemActive}`
    : styles.versionItem;
  const label = t('tasks.history.version_label', { number: version.versionNumber });
  const author = version.authorDisplayName ?? t('tasks.history.unknown_author');
  const when = formatDateTime(new Date(version.createdAt * 1000).toISOString(), locale);
  return (
    <button type="button" className={className} onClick={onSelect}>
      <span className={styles.versionLabel}>{label}</span>
      <span className={styles.versionMeta}>
        {author} · {when}
      </span>
    </button>
  );
}

interface VersionPreviewProps {
  taskId: string;
  versionId: string;
  currentBody: string;
  onRestore: (versionId: string) => void;
  restorePending: boolean;
}

function VersionPreview({
  taskId,
  versionId,
  currentBody,
  onRestore,
  restorePending,
}: VersionPreviewProps): ReactElement {
  const { t } = useTranslation('common');
  const { data: version } = useDescriptionVersionQuery(taskId, versionId);

  const lines: DiffLine[] = diffLines(version.body, currentBody);
  const noChanges = lines.every((line) => line.op === 'equal');

  return (
    <>
      <header className={styles.previewHeader}>
        <h3 className={styles.previewTitle}>
          {t('tasks.history.version_label', { number: version.versionNumber })}
        </h3>
        <Button
          type="button"
          variant="primary"
          size="sm"
          onClick={() => onRestore(version.id)}
          disabled={restorePending}
        >
          {t('tasks.history.restore')}
        </Button>
      </header>
      {noChanges ? (
        <p className={styles.previewMeta}>{t('tasks.history.no_changes')}</p>
      ) : (
        <pre className={styles.diff}>
          {lines.map((line, idx) => {
            const cls =
              line.op === 'added'
                ? styles.diffLineAdded
                : line.op === 'removed'
                  ? styles.diffLineRemoved
                  : styles.diffLineEqual;
            const prefix = line.op === 'added' ? '+ ' : line.op === 'removed' ? '- ' : '  ';
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: line position is the natural key
              <span key={idx} className={cls}>
                {prefix}
                {line.text}
                {'\n'}
              </span>
            );
          })}
        </pre>
      )}
    </>
  );
}
