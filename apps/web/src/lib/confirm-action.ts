/**
 * confirmAction — thin wrapper around the themed confirm primitive.
 *
 * Resolves the shared `common.confirm_title` / `common.confirm` /
 * `common.cancel` labels so call sites only need to pass their already-
 * translated message (and optional tone). Callers must `await` the result.
 */

import { confirm } from '@nodate-flow/ui/primitives/confirm';
import type { ReactNode } from 'react';
import { i18n } from '../i18n';

export interface ConfirmActionOptions {
  message: ReactNode;
  title?: ReactNode;
  tone?: 'neutral' | 'danger';
}

export function confirmAction(options: ConfirmActionOptions): Promise<boolean> {
  const t = i18n.getFixedT(null, 'common');
  return confirm.ask({
    title: options.title ?? t('common.confirm_title'),
    message: options.message,
    confirmLabel: t('common.confirm'),
    cancelLabel: t('common.cancel'),
    tone: options.tone ?? 'danger',
  });
}
