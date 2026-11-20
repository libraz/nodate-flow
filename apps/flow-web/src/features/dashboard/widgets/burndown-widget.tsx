/**
 * BurndownWidget — placeholder for a chart that has no data source yet.
 * Existing burndown widgets keep rendering; the type is not offered when
 * adding a widget.
 */

import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { WidgetMessage } from './widget-parts';

export default function BurndownWidget(): ReactElement {
  const { t } = useTranslation('dashboard');
  return (
    <WidgetMessage
      title={t('widgets.burndown.coming_soon_title')}
      hint={t('widgets.burndown.coming_soon_hint')}
    />
  );
}
