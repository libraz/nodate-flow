/**
 * ColumnHelp: a small "?" after a table header or inline label whose
 * meaning is not obvious from the word alone. Hover or focus shows the
 * explanation; screen readers get it as the button's name.
 */

import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { CircleHelp } from 'lucide-react';
import type { ReactElement } from 'react';

import styles from './column-help.module.css';

export default function ColumnHelp({ text }: { text: string }): ReactElement {
  return (
    <Tooltip content={text}>
      <button type="button" className={styles.trigger} aria-label={text}>
        <CircleHelp size={14} aria-hidden="true" />
      </button>
    </Tooltip>
  );
}
