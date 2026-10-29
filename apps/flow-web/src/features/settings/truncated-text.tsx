/**
 * TruncatedText — a single-line value clipped with an ellipsis, with the
 * full value in a tooltip. The full text stays in the DOM, so assistive
 * technology reads it whole regardless of the clip.
 */

import { cx } from '@nodate-flow/ui/lib/cx';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import type { ReactElement } from 'react';

import styles from './truncated-text.module.css';

export interface TruncatedTextProps {
  text: string;
  className?: string | undefined;
}

export default function TruncatedText({ text, className }: TruncatedTextProps): ReactElement {
  return (
    <Tooltip content={text}>
      <span className={cx(styles.root, className)}>{text}</span>
    </Tooltip>
  );
}
