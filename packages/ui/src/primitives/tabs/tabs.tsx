/**
 * Tabs — accessible tablist with roving tabindex.
 *
 * Implements the WAI-ARIA tabs pattern: `role="tablist"` + `role="tab"` +
 * `role="tabpanel"`, Left/Right/Home/End keyboard navigation, and a roving
 * tabindex so only the active tab is in the tab sequence.
 *
 * Controlled vs uncontrolled:
 * - Pass `value` + `onValueChange` (or the legacy `onChange` alias) to run
 *   in controlled mode — the consumer owns the active value and is
 *   responsible for persisting it (e.g. URL query params, global state).
 * - Pass `defaultValue` only to run in uncontrolled mode — the component
 *   tracks the active value internally.
 * - When `value` is provided, it takes precedence over `defaultValue`.
 * - When both `onValueChange` and `onChange` are provided, `onValueChange`
 *   takes precedence; `onChange` is retained for backward compatibility.
 */

import {
  forwardRef,
  type HTMLAttributes,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  useCallback,
  useId,
  useRef,
} from 'react';
import { useControllableState } from '../../hooks/use-controllable-state';
import { cx } from '../../lib/cx';
import styles from './tabs.module.css';

export interface TabItem {
  /** Stable identifier for the tab. */
  value: string;
  /** Already-translated tab label. */
  label: ReactNode;
  /** Panel content. */
  content: ReactNode;
  /** When true, the tab cannot be activated. */
  disabled?: boolean;
}

export interface TabsProps
  extends Omit<HTMLAttributes<HTMLDivElement>, 'onChange' | 'defaultValue'> {
  /** Tab items in display order. */
  items: TabItem[];
  /**
   * Controlled active value. When provided, the component renders whichever
   * tab matches this value and delegates persistence of state changes to
   * `onValueChange` (or the legacy `onChange` alias). Takes precedence over
   * `defaultValue`.
   */
  value?: string;
  /**
   * Default active value for uncontrolled usage. Ignored when `value` is
   * provided. Retained for backward compatibility with existing call sites.
   */
  defaultValue?: string;
  /**
   * Fired when the user activates a tab (click or keyboard). In controlled
   * mode (`value` provided), the consumer is responsible for persisting the
   * new value — e.g. writing it to URL search params. Takes precedence over
   * `onChange` when both are supplied.
   */
  onValueChange?: (value: string) => void;
  /**
   * Legacy change handler. Prefer `onValueChange` for new code; this alias
   * is preserved for backward compatibility with existing consumers.
   */
  onChange?: (value: string) => void;
  /** Already-translated accessible label for the tablist. */
  'aria-label'?: string;
}

/** Tabs renders an accessible tablist with roving tabindex. */
const Tabs = forwardRef<HTMLDivElement, TabsProps>(
  (
    {
      items,
      value,
      defaultValue,
      onValueChange,
      onChange,
      className,
      'aria-label': ariaLabel,
      ...rest
    },
    ref,
  ): ReactElement => {
    const fallback = items[0]?.value ?? '';
    // `onValueChange` takes precedence; `onChange` is preserved for backward
    // compatibility with existing consumers.
    const handleChange = onValueChange ?? onChange;
    const [active, setActive] = useControllableState<string>({
      value,
      defaultValue: defaultValue ?? fallback,
      onChange: handleChange,
    });
    const current = active ?? fallback;
    const baseId = useId();
    const tabRefs = useRef<Map<string, HTMLButtonElement>>(new Map());

    const enabledItems = items.filter((it) => !it.disabled);

    const focusValue = useCallback((next: string) => {
      const node = tabRefs.current.get(next);
      node?.focus();
    }, []);

    const onKeyDown = useCallback(
      (event: KeyboardEvent<HTMLButtonElement>, idx: number) => {
        if (enabledItems.length === 0) return;
        const currentEnabledIdx = enabledItems.findIndex(
          (it) => it.value === enabledItems[idx]?.value,
        );
        let nextIdx = currentEnabledIdx;
        switch (event.key) {
          case 'ArrowRight':
            nextIdx = (currentEnabledIdx + 1) % enabledItems.length;
            break;
          case 'ArrowLeft':
            nextIdx = (currentEnabledIdx - 1 + enabledItems.length) % enabledItems.length;
            break;
          case 'Home':
            nextIdx = 0;
            break;
          case 'End':
            nextIdx = enabledItems.length - 1;
            break;
          default:
            return;
        }
        event.preventDefault();
        const nextItem = enabledItems[nextIdx];
        if (!nextItem) return;
        setActive(nextItem.value);
        focusValue(nextItem.value);
      },
      [enabledItems, focusValue, setActive],
    );

    return (
      <div ref={ref} className={cx(styles.root, className)} {...rest}>
        <div role="tablist" aria-label={ariaLabel} className={styles.tablist}>
          {enabledItems.map((item, idx) => {
            const selected = item.value === current;
            const tabId = `${baseId}-tab-${item.value}`;
            const panelId = `${baseId}-panel-${item.value}`;
            return (
              <button
                key={item.value}
                ref={(node) => {
                  if (node) {
                    tabRefs.current.set(item.value, node);
                  } else {
                    tabRefs.current.delete(item.value);
                  }
                }}
                type="button"
                role="tab"
                id={tabId}
                aria-selected={selected}
                aria-controls={panelId}
                tabIndex={selected ? 0 : -1}
                className={cx(styles.tab, selected && styles.tabActive)}
                onClick={() => setActive(item.value)}
                onKeyDown={(e) => onKeyDown(e, idx)}
              >
                {item.label}
              </button>
            );
          })}
        </div>
        {enabledItems.map((item) => {
          const selected = item.value === current;
          const tabId = `${baseId}-tab-${item.value}`;
          const panelId = `${baseId}-panel-${item.value}`;
          return (
            <div
              key={item.value}
              role="tabpanel"
              id={panelId}
              aria-labelledby={tabId}
              hidden={!selected}
              className={styles.panel}
            >
              {selected ? item.content : null}
            </div>
          );
        })}
      </div>
    );
  },
);
Tabs.displayName = 'Tabs';

export default Tabs;
