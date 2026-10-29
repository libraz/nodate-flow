/**
 * CopyButton — writes a value to the clipboard and confirms in place.
 *
 * The label swaps to the "copied" wording with a check for a moment,
 * then returns. Both labels occupy the same grid cell so the button
 * keeps its width across the swap. A failed write is handed to
 * `onError`; the caller decides how to report it.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Icon from '@nodate-flow/ui/icon';
import Button, { type ButtonProps } from '@nodate-flow/ui/primitives/button';
import { Check, Copy } from 'lucide-react';
import { type ReactElement, useEffect, useState } from 'react';

import styles from './copy-button.module.css';

/** How long the confirmation stays before the label returns. */
const CONFIRM_MS = 2000;

export interface CopyButtonProps {
  value: string;
  label: string;
  copiedLabel: string;
  onError?: (err: unknown) => void;
  onCopied?: () => void;
  variant?: ButtonProps['variant'];
  size?: ButtonProps['size'];
  disabled?: boolean;
}

export default function CopyButton({
  value,
  label,
  copiedLabel,
  onError,
  onCopied,
  variant = 'default',
  size = 'md',
  disabled = false,
}: CopyButtonProps): ReactElement {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), CONFIRM_MS);
    return () => clearTimeout(timer);
  }, [copied]);

  const handleClick = async (): Promise<void> => {
    if (value === '') return;
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      onCopied?.();
    } catch (err) {
      onError?.(err);
    }
  };

  return (
    <>
      <Button
        type="button"
        variant={variant}
        size={size}
        disabled={disabled}
        data-copied={copied || undefined}
        className={styles.root}
        onClick={() => {
          void handleClick();
        }}
      >
        <span className={styles.stack}>
          <span className={styles.face} aria-hidden={copied}>
            <Icon icon={Copy} decorative className={styles.icon} />
            {label}
          </span>
          <span className={styles.face} aria-hidden={!copied}>
            <Icon icon={Check} decorative className={styles.icon} />
            {copiedLabel}
          </span>
        </span>
      </Button>
      {/* Outside the button so the announcement is not part of its name. */}
      <VisuallyHidden>
        <span role="status">{copied ? copiedLabel : ''}</span>
      </VisuallyHidden>
    </>
  );
}
