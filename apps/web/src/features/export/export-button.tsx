/**
 * ExportButton — toolbar button that opens the export dialog.
 */

import Icon from '@nodate-flow/ui/icon';
import Button from '@nodate-flow/ui/primitives/button';
import { Download } from 'lucide-react';
import { type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import ExportDialog from './export-dialog';

export interface ExportButtonProps {
  workspaceId: string;
  /** Active lens ID, if any — enables the "Current view" scope option. */
  activeLensId?: string;
}

export default function ExportButton({
  workspaceId,
  activeLensId,
}: ExportButtonProps): ReactElement {
  const { t } = useTranslation('export');
  const [open, setOpen] = useState(false);

  const handleOpen = (): void => {
    setOpen(true);
  };

  const handleClose = (): void => {
    setOpen(false);
  };

  return (
    <>
      <Button type="button" variant="ghost" onClick={handleOpen} aria-label={t('button')}>
        <Icon icon={Download} decorative />
        {t('button')}
      </Button>
      <ExportDialog
        workspaceId={workspaceId}
        activeLensId={activeLensId}
        open={open}
        onClose={handleClose}
      />
    </>
  );
}
