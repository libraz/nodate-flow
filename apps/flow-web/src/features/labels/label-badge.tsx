/**
 * LabelBadge — renders a single label as a coloured chip.
 */

import Chip, { type ChipSize } from '@nodate-flow/ui/primitives/chip';
import type { ReactElement, Ref } from 'react';
import { useTranslation } from 'react-i18next';

export interface LabelBadgeProps {
  name: string;
  color: string;
  onRemove?: () => void;
  /** `sm` matches Badge's height, for dense rows such as board cards. */
  size?: ChipSize;
  /** Receives the chip element, for callers that manage focus after removal. */
  ref?: Ref<HTMLSpanElement>;
}

export default function LabelBadge({
  name,
  color,
  onRemove,
  size = 'md',
  ref,
}: LabelBadgeProps): ReactElement {
  const { t } = useTranslation('labels');
  return (
    <Chip
      ref={ref}
      color={color}
      size={size}
      {...(onRemove ? { onDismiss: onRemove, dismissLabel: t('badge.remove', { name }) } : {})}
    >
      {name}
    </Chip>
  );
}
