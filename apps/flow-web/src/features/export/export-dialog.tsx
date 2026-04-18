/**
 * ExportDialog — modal for exporting tasks in CSV or JSON format.
 *
 * Allows selecting the export format and optionally scoping to the
 * currently active lens (saved view). Shows a toast on success or
 * failure.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { type ExportFormat, useExportTasks } from './api';
import styles from './export.module.css';

export interface ExportDialogProps {
  workspaceId: string;
  /** Active lens ID, if any — enables the "Current view" scope option. */
  activeLensId?: string | undefined;
  open: boolean;
  onClose: () => void;
}

export default function ExportDialog({
  workspaceId,
  activeLensId,
  open,
  onClose,
}: ExportDialogProps): ReactElement {
  const { t } = useTranslation('export');
  const { t: tc } = useTranslation('common');
  const exportMutation = useExportTasks();

  const [format, setFormat] = useState<ExportFormat>('csv');
  const [scopeToLens, setScopeToLens] = useState(false);

  const handleClose = (): void => {
    if (exportMutation.isPending) return;
    onClose();
  };

  const handleExport = async (): Promise<void> => {
    toaster.show({ tone: 'info', message: t('toast.started') });
    try {
      await exportMutation.mutateAsync({
        workspaceId,
        format,
        lensId: scopeToLens && activeLensId ? activeLensId : undefined,
      });
      toaster.show({ tone: 'success', message: t('toast.completed') });
      onClose();
    } catch {
      toaster.show({ tone: 'danger', message: t('toast.failed') });
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('title')}>
      <div className={styles.dialog}>
        {/* Format selection */}
        <fieldset className={styles.fieldGroup}>
          <legend className={styles.fieldLabel}>{t('format_label')}</legend>
          <div className={styles.radioGroup}>
            <label className={styles.radioOption}>
              <input
                type="radio"
                name="exportFormat"
                value="csv"
                checked={format === 'csv'}
                onChange={() => {
                  setFormat('csv');
                }}
              />
              {t('format.csv')}
            </label>
            <label className={styles.radioOption}>
              <input
                type="radio"
                name="exportFormat"
                value="json"
                checked={format === 'json'}
                onChange={() => {
                  setFormat('json');
                }}
              />
              {t('format.json')}
            </label>
          </div>
        </fieldset>

        {/* Scope selection — only shown when a lens is active */}
        <fieldset className={styles.fieldGroup}>
          <div className={styles.radioGroup}>
            <label className={styles.radioOption}>
              <input
                type="radio"
                name="exportScope"
                value="all"
                checked={!scopeToLens}
                onChange={() => {
                  setScopeToLens(false);
                }}
              />
              {t('scope.all')}
            </label>
            {activeLensId ? (
              <label className={styles.radioOption}>
                <input
                  type="radio"
                  name="exportScope"
                  value="lens"
                  checked={scopeToLens}
                  onChange={() => {
                    setScopeToLens(true);
                  }}
                />
                {t('scope.lens')}
              </label>
            ) : null}
          </div>
        </fieldset>

        {/* Actions */}
        <div className={styles.actions}>
          <Button
            type="button"
            variant="ghost"
            onClick={handleClose}
            disabled={exportMutation.isPending}
          >
            {tc('actions.cancel')}
          </Button>
          <Button
            type="button"
            variant="primary"
            onClick={() => {
              void handleExport();
            }}
            disabled={exportMutation.isPending}
          >
            {t('button')}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
