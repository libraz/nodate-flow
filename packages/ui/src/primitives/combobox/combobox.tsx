/**
 * Combobox — accessible filterable listbox.
 *
 * Implements the WAI-ARIA combobox-with-listbox pattern: an `<input
 * role="combobox">` paired with a portal-rendered `role="listbox"`. Keyboard
 * navigation: ArrowUp/ArrowDown to move active option, Home/End to jump,
 * Enter to select, Escape to dismiss. Active option is communicated via
 * `aria-activedescendant`.
 */

import {
  FloatingPortal,
  autoUpdate,
  flip,
  offset,
  shift,
  size,
  useClick,
  useDismiss,
  useFloating,
  useFocus,
  useInteractions,
  useRole,
} from '@floating-ui/react';
import {
  type ChangeEvent,
  type FocusEvent,
  type KeyboardEvent,
  type ReactElement,
  type Ref,
  type SyntheticEvent,
  forwardRef,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useControllableState } from '../../hooks/use-controllable-state';
import { cx } from '../../lib/cx';
import styles from './combobox.module.css';

export interface ComboboxOption {
  value: string;
  /** Already-translated display label. */
  label: string;
  disabled?: boolean;
}

export interface ComboboxProps {
  /** Available options. */
  options: ComboboxOption[];
  /** Controlled selected value. */
  value?: string;
  /** Default selected value (uncontrolled). */
  defaultValue?: string;
  /** Called when selection changes. */
  onChange?: (value: string) => void;
  /** Already-translated placeholder. */
  placeholder?: string;
  /** Already-translated accessible label. */
  'aria-label'?: string;
  /** Optional id for the input. */
  id?: string;
  /** Disable the entire combobox. */
  disabled?: boolean;
  className?: string;
}

function ComboboxImpl(
  {
    options,
    value,
    defaultValue,
    onChange,
    placeholder,
    'aria-label': ariaLabel,
    id: idProp,
    disabled,
    className,
  }: ComboboxProps,
  ref: Ref<HTMLInputElement>,
): ReactElement {
  const [selected, setSelected] = useControllableState<string>({
    value,
    defaultValue,
    onChange,
  });

  const initialLabel = useMemo(
    () => options.find((o) => o.value === selected)?.label ?? '',
    [options, selected],
  );

  const [query, setQuery] = useState(initialLabel);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState<number | null>(null);

  /**
   * Re-sync `query` to the derived `initialLabel` when the resolved label
   * changes out from under us — typically a locale switch rebuilding the
   * `options` prop with freshly translated `label` strings, or a controlled
   * `value` swap. We only overwrite when the user has not typed anything
   * since the previous sync: `query === prevInitialLabelRef.current`
   * distinguishes "untouched placeholder/selected label" from "in-progress
   * filter query". First-mount `useState(initialLabel)` still handles the
   * initial render.
   */
  const prevInitialLabelRef = useRef(initialLabel);
  useEffect(() => {
    if (prevInitialLabelRef.current === initialLabel) return;
    if (query === prevInitialLabelRef.current) {
      setQuery(initialLabel);
    }
    prevInitialLabelRef.current = initialLabel;
  }, [initialLabel, query]);
  const baseId = useId();
  const inputId = idProp ?? `${baseId}-input`;
  const listId = `${baseId}-list`;

  const filtered = useMemo(() => {
    if (!query || query === initialLabel) return options;
    const q = query.toLowerCase();
    return options.filter((o) => o.label.toLowerCase().includes(q));
  }, [options, query, initialLabel]);

  const { refs, floatingStyles, context } = useFloating({
    open,
    onOpenChange: (next) => {
      setOpen(next);
      if (next && activeIndex === null) setActiveIndex(0);
    },
    middleware: [
      offset(4),
      flip(),
      shift({ padding: 8 }),
      size({
        apply({ rects, elements }) {
          Object.assign(elements.floating.style, { minInlineSize: `${rects.reference.width}px` });
        },
      }),
    ],
    whileElementsMounted: autoUpdate,
  });

  const listRef = useRef<Array<HTMLElement | null>>([]);

  const click = useClick(context, { event: 'mousedown', keyboardHandlers: false });
  const focus = useFocus(context);
  const dismiss = useDismiss(context, { escapeKey: true, outsidePress: true });
  const role = useRole(context, { role: 'listbox' });

  const { getReferenceProps, getFloatingProps, getItemProps } = useInteractions([
    click,
    focus,
    dismiss,
    role,
  ]);

  const handleChange = (event: ChangeEvent<HTMLInputElement>): void => {
    setQuery(event.target.value);
    setOpen(true);
    setActiveIndex(0);
  };

  const select = (option: ComboboxOption): void => {
    if (option.disabled) return;
    setSelected(option.value);
    setQuery(option.label);
    setOpen(false);
    setActiveIndex(null);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>): void => {
    /**
     * IME composition guard. While an input method (kana/pinyin/etc.) owns
     * the keystroke, the resulting Enter / arrow keys belong to the
     * composition itself — committing kana, cycling candidates — not to the
     * listbox. Acting on them would (a) auto-select the top filtered option
     * on the Enter that commits the composition, and (b) then concatenate
     * the IME-committed text after the chosen label. `keyCode === 229` is
     * the legacy sentinel some browsers still emit alongside `isComposing`.
     */
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;

    if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        setActiveIndex(0);
        return;
      }
      const next = activeIndex === null ? 0 : (activeIndex + 1) % filtered.length;
      setActiveIndex(next);
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (!open) {
        setOpen(true);
        setActiveIndex(filtered.length - 1);
        return;
      }
      const prev =
        activeIndex === null || activeIndex === 0 ? filtered.length - 1 : activeIndex - 1;
      setActiveIndex(prev);
    } else if (event.key === 'Enter' && open) {
      const idx = activeIndex ?? 0;
      const option = filtered[idx];
      if (option) {
        event.preventDefault();
        select(option);
      }
    } else if (event.key === 'Home' && open) {
      event.preventDefault();
      setActiveIndex(0);
    } else if (event.key === 'End' && open) {
      event.preventDefault();
      setActiveIndex(filtered.length - 1);
    }
  };

  return (
    <div className={cx(styles.root, className)}>
      <input
        ref={(node: HTMLInputElement | null) => {
          refs.setReference(node);
          if (typeof ref === 'function') ref(node);
          else if (ref) ref.current = node;
        }}
        id={inputId}
        type="text"
        role="combobox"
        aria-label={ariaLabel}
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-autocomplete="list"
        aria-activedescendant={
          open && activeIndex !== null ? `${baseId}-opt-${activeIndex}` : undefined
        }
        autoComplete="off"
        disabled={disabled}
        className={styles.input}
        placeholder={placeholder}
        value={query}
        {...getReferenceProps({
          onChange: (event: SyntheticEvent) => handleChange(event as ChangeEvent<HTMLInputElement>),
          onKeyDown: (event: SyntheticEvent) =>
            handleKeyDown(event as KeyboardEvent<HTMLInputElement>),
          onFocus: (event: SyntheticEvent) => {
            (event as FocusEvent<HTMLInputElement>).currentTarget.select();
          },
        })}
      />
      {open && filtered.length > 0 ? (
        <FloatingPortal>
          <ul
            ref={refs.setFloating}
            id={listId}
            style={floatingStyles}
            className={styles.list}
            {...getFloatingProps()}
          >
            {filtered.map((option, idx) => {
              const active = idx === activeIndex;
              const isSelected = option.value === selected;
              return (
                <li
                  key={option.value}
                  ref={(node) => {
                    listRef.current[idx] = node;
                  }}
                  id={`${baseId}-opt-${idx}`}
                  role="option"
                  aria-selected={isSelected}
                  aria-disabled={option.disabled || undefined}
                  className={cx(
                    styles.option,
                    active && styles.optionActive,
                    isSelected && styles.optionSelected,
                  )}
                  {...getItemProps({
                    onClick: () => select(option),
                  })}
                >
                  {option.label}
                </li>
              );
            })}
          </ul>
        </FloatingPortal>
      ) : null}
    </div>
  );
}

const Combobox = forwardRef<HTMLInputElement, ComboboxProps>(ComboboxImpl);
Combobox.displayName = 'Combobox';

export default Combobox;
