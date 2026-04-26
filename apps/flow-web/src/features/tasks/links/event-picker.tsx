/**
 * EventPicker — popover combobox for choosing a calendar event to link.
 *
 * Implements the WAI-ARIA combobox + listbox + activedescendant
 * pattern: focus stays on the search input and the active row in the
 * listbox is tracked by `aria-activedescendant`. ArrowDown / ArrowUp
 * cycle through enabled rows (skipping rows whose event is already
 * linked), Enter commits the active row with the currently-selected
 * `LinkKind`, and Escape closes the popover. Tab moves focus from the
 * input to the kind selector in the footer; Shift+Tab from the input
 * closes the popover.
 *
 * Search is debounced at 200ms locally and forwarded to
 * `useEventSearch` which handles the workspace `/calendar-events`
 * fetch and the substring filter. When the query is empty the hook
 * returns the upcoming-window default suggestion list. Past events are
 * grouped under a `pastDivider` so the listbox stays scannable.
 *
 * The popover positions itself absolutely under the `popoverHost`
 * wrapper and the section header's "Link event" trigger. Outside-click
 * dismissal is handled by a single `mousedown` listener registered on
 * the document while the popover is open.
 */

import {
  type KeyboardEvent,
  type ReactElement,
  type RefObject,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { useEventSearch } from './hooks/use-event-search';
import KindSelector from './kind-selector';
import { formatPickerDate, isPast } from './linked-event-time';
import styles from './linked-events.module.css';
import type { CalendarEventListItem, LinkKind } from './types';

const DEBOUNCE_MS = 200;

export interface EventPickerProps {
  workspaceId: string;
  taskId: string;
  alreadyLinkedEventIds: ReadonlySet<string>;
  locale: string;
  /** Element the popover anchors to; used for outside-click detection. */
  anchorRef: RefObject<HTMLElement | null>;
  isOpen: boolean;
  onClose: () => void;
  onLink: (args: { event: CalendarEventListItem; kind: LinkKind }) => void;
}

interface PickerEntry {
  event: CalendarEventListItem;
  isLinked: boolean;
  isPastEvent: boolean;
}

/**
 * Build the listbox model: split entries into upcoming + past, mark
 * already-linked rows as disabled, and remember which event ids are
 * actually selectable so keyboard navigation can skip the rest.
 */
function buildEntries(
  events: readonly CalendarEventListItem[],
  alreadyLinked: ReadonlySet<string>,
): { upcoming: PickerEntry[]; past: PickerEntry[]; selectableIds: string[] } {
  const upcoming: PickerEntry[] = [];
  const past: PickerEntry[] = [];
  const selectableIds: string[] = [];
  for (const event of events) {
    if (event.id === undefined) continue;
    const isLinked = alreadyLinked.has(event.id);
    const entry: PickerEntry = {
      event,
      isLinked,
      isPastEvent: isPast(event.startAt),
    };
    if (entry.isPastEvent) past.push(entry);
    else upcoming.push(entry);
    if (!isLinked) selectableIds.push(event.id);
  }
  return { upcoming, past, selectableIds };
}

export default function EventPicker({
  workspaceId,
  alreadyLinkedEventIds,
  locale,
  anchorRef,
  isOpen,
  onClose,
  onLink,
}: EventPickerProps): ReactElement | null {
  const { t } = useTranslation('linkedEvents');
  const popoverRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listboxId = useId();
  const optionIdPrefix = useId();

  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [kind, setKind] = useState<LinkKind>('contributes_to');
  const [activeId, setActiveId] = useState<string | null>(null);

  // Debounce the input -> hook query argument (200ms).
  useEffect(() => {
    if (!isOpen) return;
    const id = window.setTimeout(() => {
      setDebouncedQuery(query.trim());
    }, DEBOUNCE_MS);
    return () => {
      window.clearTimeout(id);
    };
  }, [query, isOpen]);

  // Reset transient state every time the popover (re-)opens so the
  // user starts from a clean slate.
  useEffect(() => {
    if (!isOpen) return;
    setQuery('');
    setDebouncedQuery('');
    setKind('contributes_to');
    setActiveId(null);
    // Focus the input on the next frame so the popover finishes
    // mounting before we move the caret.
    const handle = window.requestAnimationFrame(() => {
      inputRef.current?.focus();
    });
    return () => {
      window.cancelAnimationFrame(handle);
    };
  }, [isOpen]);

  const search = useEventSearch(workspaceId, debouncedQuery, isOpen);
  const { upcoming, past, selectableIds } = useMemo(
    () => buildEntries(search.data?.events ?? [], alreadyLinkedEventIds),
    [search.data, alreadyLinkedEventIds],
  );

  // Auto-pick the first selectable row whenever the result set changes.
  useEffect(() => {
    if (selectableIds.length === 0) {
      setActiveId(null);
      return;
    }
    if (activeId !== null && selectableIds.includes(activeId)) return;
    setActiveId(selectableIds[0] ?? null);
  }, [selectableIds, activeId]);

  // Close on outside click (mousedown so we beat focus loss).
  useEffect(() => {
    if (!isOpen) return;
    const onMouseDown = (event: MouseEvent): void => {
      const target = event.target as Node | null;
      if (!target) return;
      if (popoverRef.current?.contains(target)) return;
      if (anchorRef.current?.contains(target)) return;
      onClose();
    };
    document.addEventListener('mousedown', onMouseDown);
    return () => {
      document.removeEventListener('mousedown', onMouseDown);
    };
  }, [isOpen, onClose, anchorRef]);

  if (!isOpen) return null;

  const allEntries: PickerEntry[] = [...upcoming, ...past];
  const optionIdFor = (eventId: string): string => `${optionIdPrefix}-${eventId}`;

  const moveActive = (delta: 1 | -1): void => {
    if (selectableIds.length === 0) return;
    const currentIndex = activeId !== null ? selectableIds.indexOf(activeId) : delta === 1 ? -1 : 0;
    const nextIndex = (currentIndex + delta + selectableIds.length) % selectableIds.length;
    setActiveId(selectableIds[nextIndex] ?? null);
  };

  const commitActive = (): void => {
    if (activeId === null) return;
    const entry = allEntries.find((e) => e.event.id === activeId);
    if (!entry || entry.isLinked) return;
    onLink({ event: entry.event, kind });
  };

  // Restore focus to the popover's anchor (the section header trigger)
  // when the user dismisses via keyboard. Outside-click dismissal lets
  // the browser's default focus rules apply so we don't steal focus
  // from wherever the user just clicked.
  const closeAndRestoreFocus = (): void => {
    onClose();
    anchorRef.current?.focus();
  };

  const handleInputKeyDown = (event: KeyboardEvent<HTMLInputElement>): void => {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        moveActive(1);
        break;
      case 'ArrowUp':
        event.preventDefault();
        moveActive(-1);
        break;
      case 'Enter':
        event.preventDefault();
        commitActive();
        break;
      case 'Escape':
        event.preventDefault();
        closeAndRestoreFocus();
        break;
      default:
    }
  };

  const handlePopoverKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    if (event.key === 'Escape') {
      event.preventDefault();
      closeAndRestoreFocus();
    }
  };

  const isLoading = search.isFetching && (search.data === undefined || debouncedQuery !== '');
  const hasNoMatches = search.data !== undefined && upcoming.length === 0 && past.length === 0;
  const showNoResultsCopy = hasNoMatches && debouncedQuery.length > 0;
  const showEmptyHint = hasNoMatches && debouncedQuery.length === 0;

  return (
    <div
      ref={popoverRef}
      className={styles.popover}
      // biome-ignore lint/a11y/useSemanticElements: <dialog> implies modal semantics; this is a non-modal anchored popover and must not block the surrounding page
      role="dialog"
      aria-modal="false"
      aria-label={t('trigger.linkEvent')}
      onKeyDown={handlePopoverKeyDown}
    >
      <div className={styles.popoverInputRow}>
        <svg
          className={styles.popoverInputIcon}
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth={1.5}
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <circle cx="7" cy="7" r="4.5" />
          <path d="M10.5 10.5L13 13" />
        </svg>
        <input
          ref={inputRef}
          type="text"
          role="combobox"
          aria-expanded
          aria-controls={listboxId}
          aria-autocomplete="list"
          aria-activedescendant={activeId !== null ? optionIdFor(activeId) : undefined}
          className={styles.popoverInput}
          placeholder={t('picker.searchPlaceholder')}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
          }}
          onKeyDown={handleInputKeyDown}
          autoComplete="off"
          spellCheck={false}
        />
      </div>

      {isLoading ? <p className={styles.resultsHint}>{t('picker.loading')}</p> : null}
      {showNoResultsCopy ? (
        <p className={styles.resultsHint}>{t('picker.noResults', { q: debouncedQuery })}</p>
      ) : null}
      {showEmptyHint ? <p className={styles.resultsHint}>{t('picker.searchPlaceholder')}</p> : null}

      {/* biome-ignore lint/a11y/useFocusableInteractive: focus stays on the combobox input; options are addressed via aria-activedescendant per the WAI-ARIA combobox pattern */}
      {/* biome-ignore lint/a11y/noNoninteractiveElementToInteractiveRole: combobox + listbox + activedescendant pattern requires the listbox role on a host element */}
      {/* biome-ignore lint/a11y/useSemanticElements: <select> cannot render the date/title/already-linked tri-column rows or the past-divider grouping */}
      <ul id={listboxId} role="listbox" className={styles.results}>
        {upcoming.map((entry) => (
          <ResultRow
            key={entry.event.id}
            entry={entry}
            locale={locale}
            isActive={entry.event.id === activeId}
            optionId={optionIdFor(entry.event.id ?? '')}
            onPick={() => {
              if (entry.isLinked) return;
              onLink({ event: entry.event, kind });
            }}
            onHover={() => {
              if (!entry.isLinked && entry.event.id !== undefined) setActiveId(entry.event.id);
            }}
            alreadyLinkedLabel={t('picker.alreadyLinked')}
          />
        ))}
        {past.length > 0 ? (
          <li role="presentation" className={styles.pastDivider}>
            {t('picker.pastDivider')}
          </li>
        ) : null}
        {past.map((entry) => (
          <ResultRow
            key={entry.event.id}
            entry={entry}
            locale={locale}
            isActive={entry.event.id === activeId}
            optionId={optionIdFor(entry.event.id ?? '')}
            onPick={() => {
              if (entry.isLinked) return;
              onLink({ event: entry.event, kind });
            }}
            onHover={() => {
              if (!entry.isLinked && entry.event.id !== undefined) setActiveId(entry.event.id);
            }}
            alreadyLinkedLabel={t('picker.alreadyLinked')}
          />
        ))}
      </ul>

      <div className={styles.popoverFooter}>
        <span className={styles.popoverFooterLabel}>{t('kind.label')}</span>
        <KindSelector value={kind} onChange={setKind} />
        <span className={styles.escHint} aria-hidden="true">
          {t('picker.esc')}
        </span>
      </div>
    </div>
  );
}

interface ResultRowProps {
  entry: PickerEntry;
  locale: string;
  isActive: boolean;
  optionId: string;
  onPick: () => void;
  onHover: () => void;
  alreadyLinkedLabel: string;
}

function ResultRow({
  entry,
  locale,
  isActive,
  optionId,
  onPick,
  onHover,
  alreadyLinkedLabel,
}: ResultRowProps): ReactElement {
  const { event, isLinked } = entry;
  const dateText = formatPickerDate(event.startAt, locale, event.allDay ?? false);
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard handled at the combobox input level
    // biome-ignore lint/a11y/useFocusableInteractive: activedescendant pattern keeps focus on the input; options are not individually focusable by design
    // biome-ignore lint/a11y/useSemanticElements: <option> cannot host the date/title/already-linked tri-column layout or arbitrary children
    <li
      // biome-ignore lint/a11y/noNoninteractiveElementToInteractiveRole: combobox + listbox + activedescendant pattern requires the option role on each row inside the listbox
      role="option"
      id={optionId}
      aria-selected={isActive}
      aria-disabled={isLinked}
      data-active={isActive ? 'true' : undefined}
      className={styles.resultRow}
      onMouseDown={(e) => {
        // Prevent the input from losing focus before onClick fires.
        e.preventDefault();
      }}
      onClick={() => {
        if (!isLinked) onPick();
      }}
      onMouseEnter={onHover}
    >
      <span className={styles.resultDate}>{dateText}</span>
      <span className={styles.resultTitle}>{event.title ?? ''}</span>
      {isLinked ? <span className={styles.resultLinked}>{alreadyLinkedLabel}</span> : null}
    </li>
  );
}
