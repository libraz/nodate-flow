/**
 * KindSelector — segmented radiogroup for `LinkKind` (`contributes_to`
 * vs `blocks`).
 *
 * Implements the WAI-ARIA radio group keyboard pattern: ArrowRight
 * advances to the next option and ArrowLeft returns to the previous
 * one, both wrapping at the ends; Home / End jump to the extremes; the
 * selection moves with the focus so the value is updated in real time
 * (as opposed to requiring an extra Space / Enter press).
 *
 * The two options are rendered in a fixed order — `contributes_to` on
 * the inline-start, `blocks` on the inline-end — across en / ja / zh.
 * The pill rendering and active-state highlight come entirely from the
 * shared section CSS module.
 */

import { type KeyboardEvent, type ReactElement, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import LinkedEventGlyph from './linked-event-glyph';
import styles from './linked-events.module.css';
import type { LinkKind } from './types';

const ORDER: readonly LinkKind[] = ['contributes_to', 'blocks'];

export interface KindSelectorProps {
  value: LinkKind;
  onChange: (next: LinkKind) => void;
  /** Optional id prefix; resolves a stable id for each option button. */
  idPrefix?: string;
}

export default function KindSelector({
  value,
  onChange,
  idPrefix,
}: KindSelectorProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  const generatedPrefix = useId();
  const prefix = idPrefix ?? generatedPrefix;
  const refs = useRef<Map<LinkKind, HTMLButtonElement | null>>(
    new Map<LinkKind, HTMLButtonElement | null>([
      ['contributes_to', null],
      ['blocks', null],
    ]),
  );

  const focusKind = (kind: LinkKind): void => {
    refs.current.get(kind)?.focus();
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLButtonElement>): void => {
    const currentIndex = ORDER.indexOf(value);
    if (currentIndex < 0) return;
    let nextIndex = currentIndex;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        nextIndex = (currentIndex + 1) % ORDER.length;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        nextIndex = (currentIndex - 1 + ORDER.length) % ORDER.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = ORDER.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const nextKind = ORDER[nextIndex];
    if (nextKind === undefined || nextKind === value) return;
    onChange(nextKind);
    focusKind(nextKind);
  };

  return (
    <div role="radiogroup" aria-label={t('kind.label')} className={styles.kindGroup}>
      {ORDER.map((kind) => {
        const checked = kind === value;
        const label = kind === 'blocks' ? t('kind.blocks') : t('kind.contributesTo');
        return (
          <button
            key={kind}
            ref={(el) => {
              refs.current.set(kind, el);
            }}
            id={`${prefix}-${kind}`}
            type="button"
            role="radio"
            aria-checked={checked}
            tabIndex={checked ? 0 : -1}
            className={styles.kindOption}
            onClick={() => {
              if (!checked) onChange(kind);
            }}
            onKeyDown={handleKeyDown}
          >
            <LinkedEventGlyph
              kind={kind}
              {...(styles.kindGlyph !== undefined ? { className: styles.kindGlyph } : {})}
            />
            <span>{label}</span>
          </button>
        );
      })}
    </div>
  );
}
