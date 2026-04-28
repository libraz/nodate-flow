/**
 * SegmentedControl — accessible radiogroup for small ordinal enums.
 *
 * This primitive is the preferred replacement for a native `<select>` when the
 * value set is small (2–5), fixed, and benefits from having every option
 * visible at once (e.g. priority: none / low / medium / high / urgent, or a
 * view toggle: month / week / day). It renders a `<div role="radiogroup">`
 * containing `<button role="radio">` children, implements the WAI-ARIA APG
 * radiogroup pattern with a roving tabindex, and supports ArrowLeft /
 * ArrowRight / Home / End for focus + activation.
 *
 * The component is strictly controlled: callers must provide both `value` and
 * `onChange`. Visual styling leans on design tokens (`--nf-color-accent*`,
 * `--nf-color-fg-muted`, etc.) so all four themes inherit correctly. When
 * `colourful` is true, each segment maps its `tone` to the standard tone
 * tokens (`--nf-color-success`, `--nf-color-warning`, ...), using the
 * `-subtle` variant for inactive state and the full-saturation variant for
 * the active segment; this is intended for ordinal colour encoding such as
 * priority pickers.
 */

import {
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  useCallback,
  useMemo,
  useRef,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './segmented-control.module.css';

/**
 * Visual / semantic tone for a single segment.
 *
 * Tone families:
 * - Status tones (`'info' | 'success' | 'warning' | 'danger'`) map to the
 *   generic `--nf-color-<status>` / `-subtle` tokens and are intended for
 *   ordinal severity or state selectors.
 * - Calendar-kind tones (`'task' | 'event' | 'block' | 'free' | 'milestone'`)
 *   map to the domain-specific `--nf-cal-<kind>-color` / `-subtle` tokens
 *   and are intended for the unified calendar event create/edit dialog's
 *   kind picker.
 * - `'neutral'` is the default fallback and uses the group accent tokens.
 */
export type SegmentedControlTone =
  | 'neutral'
  | 'info'
  | 'success'
  | 'warning'
  | 'danger'
  | 'task'
  | 'event'
  | 'block'
  | 'free'
  | 'milestone';

/** Size variant for the whole group. */
export type SegmentedControlSize = 'sm' | 'md';

/**
 * Describes a single segment. `T` is the string-union type of the underlying
 * enum so the `value` prop stays exhaustive across all segments.
 */
export interface SegmentedControlOption<T extends string> {
  /** Stable identifier for the segment (must match one of the enum values). */
  value: T;
  /** Rendered label content (string, icon, or composition). */
  label: ReactNode;
  /**
   * Optional per-segment colour hint used when `colourful` is true on the
   * parent. Ignored otherwise; the group's accent is used instead. Defaults
   * to `'neutral'` when omitted.
   */
  tone?: SegmentedControlTone;
  /** When true, the segment cannot be activated and is skipped on keyboard nav. */
  disabled?: boolean;
  /**
   * Overrides the visible label for a11y. Required when `label` is purely
   * iconographic (no readable text).
   */
  ariaLabel?: string;
}

/** Props for {@link SegmentedControl}. */
export interface SegmentedControlProps<T extends string> {
  /** Currently selected segment value (controlled). */
  value: T;
  /** Called with the next value when the user activates a different segment. */
  onChange: (next: T) => void;
  /** Segment definitions, in display order. */
  options: SegmentedControlOption<T>[];
  /** Already-translated accessible name for the whole group. */
  ariaLabel: string;
  /** Size variant. Defaults to `'md'`. */
  size?: SegmentedControlSize;
  /**
   * When true, each segment's background reflects its `tone` even when
   * inactive (subtle tint). Active segment uses the full-saturation tone.
   * Intended for ordinal colour encoding. Defaults to `false`.
   */
  colourful?: boolean;
  /** Disables the entire group. */
  disabled?: boolean;
  /**
   * When true, the control fills the parent's inline size and each segment
   * gets equal width (`flex: 1 1 0`). Use for top-level pickers (kind / mode /
   * view tabs) where intrinsic-width segments would otherwise produce a
   * visibly uneven row due to label length variance (notably in Japanese,
   * where a single row can mix 2-char and 7-char labels). Leave false
   * (default) for inline toggles whose intrinsic width should hug content.
   */
  fullWidth?: boolean;
  /** Root className pass-through for layout / alignment customisation. */
  className?: string;
  /** Root inline style pass-through. */
  style?: CSSProperties;
}

/**
 * Resolve a plain-text accessible name from a React node when possible.
 * Used to fall back from an explicit `ariaLabel` to the string label for
 * purely-textual segments.
 */
function stringLabel(node: ReactNode): string | undefined {
  if (typeof node === 'string') return node;
  if (typeof node === 'number') return String(node);
  return undefined;
}

/**
 * Internal: map a tone to the CSS-module class that drives its inactive /
 * active background + foreground variables. The module CSS holds the actual
 * token bindings; this function only selects the right class name.
 */
function toneClass(tone: SegmentedControlTone | undefined): string | undefined {
  switch (tone) {
    case 'info':
      return styles.toneInfo;
    case 'success':
      return styles.toneSuccess;
    case 'warning':
      return styles.toneWarning;
    case 'danger':
      return styles.toneDanger;
    case 'task':
      return styles.toneTask;
    case 'event':
      return styles.toneEvent;
    case 'block':
      return styles.toneBlock;
    case 'free':
      return styles.toneFree;
    case 'milestone':
      return styles.toneMilestone;
    case 'neutral':
    case undefined:
      return styles.toneNeutral;
    default:
      return styles.toneNeutral;
  }
}

/**
 * SegmentedControl — see file-level doc block.
 *
 * Note: this component is generic. We do not use `React.forwardRef` because
 * `forwardRef` erases the `T` generic; consumers that need the root DOM
 * element can wrap with their own ref or use the event target. The typical
 * use-case (ordinal enum picker) doesn't need an external ref.
 */
/**
 * Read the effective writing direction at the moment of a keystroke. Walks
 * up from the keypress target looking for an explicit `dir` attribute so a
 * locally scoped RTL island (e.g. a single Arabic-language form region in an
 * otherwise English document) navigates correctly; falls back to
 * `document.documentElement.dir`, then `'ltr'`. Resolved per-event rather
 * than via `useSyncExternalStore` so we never need a re-render to pick up a
 * direction change — the next keystroke after the change reads correctly.
 */
function readEffectiveDir(node: Element | null): 'rtl' | 'ltr' {
  if (node) {
    const closest = node.closest('[dir]');
    if (closest instanceof HTMLElement && closest.dir) {
      return closest.dir === 'rtl' ? 'rtl' : 'ltr';
    }
  }
  if (typeof document !== 'undefined' && document.documentElement.dir === 'rtl') {
    return 'rtl';
  }
  return 'ltr';
}

function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
  ariaLabel,
  size = 'md',
  colourful = false,
  disabled = false,
  fullWidth = false,
  className,
  style,
}: SegmentedControlProps<T>): ReactElement {
  const buttonRefs = useRef<Map<T, HTMLButtonElement>>(new Map());
  const rootRef = useRef<HTMLDivElement | null>(null);

  // Pre-compute the list of enabled segments so keyboard navigation skips
  // disabled entries without repeatedly filtering inside the handler.
  const enabledOptions = useMemo(() => options.filter((opt) => !opt.disabled), [options]);

  const focusValue = useCallback((next: T) => {
    const node = buttonRefs.current.get(next);
    node?.focus();
  }, []);

  const activate = useCallback(
    (next: T) => {
      if (next === value) return;
      onChange(next);
    },
    [onChange, value],
  );

  const onKeyDown = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>, focused: T) => {
      if (enabledOptions.length === 0) return;

      // Space / Enter activate the focused segment (radios do NOT toggle;
      // selection is an explicit activation of the focused one).
      if (event.key === ' ' || event.key === 'Enter') {
        event.preventDefault();
        activate(focused);
        return;
      }

      const currentIdx = enabledOptions.findIndex((opt) => opt.value === focused);
      if (currentIdx < 0) return;

      // In RTL, the visual order of segments is mirrored: the segment at the
      // start of the array sits on the right. Honour the user's spatial
      // expectation by inverting ArrowLeft / ArrowRight (ArrowUp / ArrowDown
      // remain logical and ignore direction — they map to prev / next).
      const isRtl = readEffectiveDir(event.currentTarget) === 'rtl';
      let nextIdx = currentIdx;
      switch (event.key) {
        case 'ArrowRight':
          nextIdx = isRtl
            ? (currentIdx - 1 + enabledOptions.length) % enabledOptions.length
            : (currentIdx + 1) % enabledOptions.length;
          break;
        case 'ArrowDown':
          nextIdx = (currentIdx + 1) % enabledOptions.length;
          break;
        case 'ArrowLeft':
          nextIdx = isRtl
            ? (currentIdx + 1) % enabledOptions.length
            : (currentIdx - 1 + enabledOptions.length) % enabledOptions.length;
          break;
        case 'ArrowUp':
          nextIdx = (currentIdx - 1 + enabledOptions.length) % enabledOptions.length;
          break;
        case 'Home':
          nextIdx = 0;
          break;
        case 'End':
          nextIdx = enabledOptions.length - 1;
          break;
        default:
          return;
      }
      event.preventDefault();
      const nextItem = enabledOptions[nextIdx];
      if (!nextItem) return;
      // In the APG radiogroup pattern, arrow keys move focus AND change the
      // selected value immediately. This matches the behaviour consumers get
      // from native `<input type="radio">` groups.
      activate(nextItem.value);
      focusValue(nextItem.value);
    },
    [activate, enabledOptions, focusValue],
  );

  // The "roving tabindex focal point" is the active segment, falling back to
  // the first enabled segment when the active one happens to be disabled.
  const rovingTarget: T | undefined = useMemo(() => {
    const active = options.find((opt) => opt.value === value && !opt.disabled);
    if (active) return active.value;
    return enabledOptions[0]?.value;
  }, [enabledOptions, options, value]);

  return (
    <div
      ref={rootRef}
      role="radiogroup"
      aria-label={ariaLabel}
      aria-disabled={disabled || undefined}
      data-size={size}
      data-colourful={colourful ? 'true' : undefined}
      data-full-width={fullWidth ? 'true' : undefined}
      className={cx(
        styles.root,
        styles[`size-${size}`],
        fullWidth && styles.rootFullWidth,
        className,
      )}
      style={style}
    >
      {options.map((option) => {
        const selected = option.value === value;
        const isDisabled = disabled || Boolean(option.disabled);
        const tabIndex = rovingTarget === option.value ? 0 : -1;
        const label = option.ariaLabel ?? stringLabel(option.label) ?? undefined;

        return (
          <button
            key={option.value}
            ref={(node) => {
              if (node) {
                buttonRefs.current.set(option.value, node);
              } else {
                buttonRefs.current.delete(option.value);
              }
            }}
            type="button"
            role="radio"
            aria-checked={selected}
            aria-label={label}
            disabled={isDisabled}
            tabIndex={tabIndex}
            data-selected={selected ? 'true' : 'false'}
            data-tone={option.tone ?? 'neutral'}
            className={cx(
              styles.segment,
              selected && styles.segmentActive,
              colourful && styles.segmentColourful,
              colourful && toneClass(option.tone),
              fullWidth && styles.segmentFullWidth,
            )}
            onClick={() => activate(option.value)}
            onKeyDown={(e) => onKeyDown(e, option.value)}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

export { SegmentedControl };
export default SegmentedControl;
