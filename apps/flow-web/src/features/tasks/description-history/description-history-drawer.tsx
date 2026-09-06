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
 *
 * The diff is computed against the stored text — an edit that only
 * changes which person a mention points at is a change, and has to read
 * as one — but every segment is drawn through the shared body renderer,
 * so the reader sees the name rather than the notation and the id it
 * carries.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Button from '@nodate-flow/ui/primitives/button';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { X } from 'lucide-react';
import { type ReactElement, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../../lib/api-error';
import { formatDateTime } from '../../../lib/format';
import {
  type DescriptionVersion,
  useDescriptionHistoryQuery,
  useDescriptionVersionQuery,
  useRestoreDescriptionVersion,
} from '../description-history-api';
import styles from './description-history-drawer.module.css';
import { type DiffBlock, diffLines, groupDiffLines } from './diff';

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
      <div className={styles.header}>
        <button
          type="button"
          className={styles.close}
          aria-label={t('tasks.history.close')}
          onClick={onClose}
        >
          <X size={16} aria-hidden="true" />
        </button>
      </div>
      <Suspense
        fallback={
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
            <Skeleton style={{ blockSize: '2rem', inlineSize: '100%' }} />
            {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
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
          const message = formatApiError(err, t, 'tasks.history.restore_error');
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
              <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
                {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
                <Skeleton style={{ blockSize: '1.5rem', inlineSize: '50%' }} />
                {/* nf-token-override: placeholder sized to the content it stands in for, not a spacing step */}
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

  const blocks: DiffBlock[] = groupDiffLines(diffLines(version.body, currentBody));
  const noChanges = blocks.every((block) => block.op === 'equal');

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
        <>
          <DiffLegend versionNumber={version.versionNumber} />
          <div className={styles.diff}>
            {blocks.map((block, idx) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: block position is the natural key
              <DiffBlockRow key={idx} block={block} versionNumber={version.versionNumber} />
            ))}
          </div>
        </>
      )}
    </>
  );
}

/**
 * Says in words which revision each marker stands for. The two sides are
 * also drawn in different colours, which is the faster read for anyone
 * who sees the difference — but a marker and a sentence are what carry
 * the meaning, so nothing depends on telling green from red.
 */
function DiffLegend({ versionNumber }: { versionNumber: number }): ReactElement {
  const { t } = useTranslation('common');
  return (
    <ul className={styles.legend} aria-label={t('tasks.history.diff.legend_label')}>
      <li className={styles.legendItem}>
        <span className={`${styles.marker} ${styles.markerAdded}`} aria-hidden="true">
          +
        </span>
        {t('tasks.history.diff.added', { number: versionNumber })}
      </li>
      <li className={styles.legendItem}>
        <span className={`${styles.marker} ${styles.markerRemoved}`} aria-hidden="true">
          -
        </span>
        {t('tasks.history.diff.removed')}
      </li>
    </ul>
  );
}

interface DiffBlockRowProps {
  block: DiffBlock;
  versionNumber: number;
}

function DiffBlockRow({ block, versionNumber }: DiffBlockRowProps): ReactElement {
  const { t } = useTranslation('common');

  const rowClass =
    block.op === 'added'
      ? `${styles.diffBlock} ${styles.diffBlockAdded}`
      : block.op === 'removed'
        ? `${styles.diffBlock} ${styles.diffBlockRemoved}`
        : styles.diffBlock;
  const markerClass =
    block.op === 'added'
      ? `${styles.marker} ${styles.markerAdded}`
      : block.op === 'removed'
        ? `${styles.marker} ${styles.markerRemoved}`
        : styles.marker;
  const marker = block.op === 'added' ? '+' : block.op === 'removed' ? '-' : '';
  const sideLabel =
    block.op === 'added'
      ? t('tasks.history.diff.added_side', { number: versionNumber })
      : block.op === 'removed'
        ? t('tasks.history.diff.removed_side')
        : null;

  return (
    <div className={rowClass}>
      <span className={markerClass} aria-hidden="true">
        {marker}
      </span>
      <div className={styles.diffContent}>
        {sideLabel === null ? null : <VisuallyHidden>{sideLabel}</VisuallyHidden>}
        <Markdown>{block.text}</Markdown>
      </div>
    </div>
  );
}
