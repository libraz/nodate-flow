/**
 * MentionTextarea — a textarea that offers the people of a workspace when
 * the author types `@`, and writes the stable mention notation on
 * selection.
 *
 * The picker is a WAI-ARIA combobox: the textarea carries the combobox
 * role while the list is open, the list is a `listbox`, and the row the
 * arrow keys are on is named by `aria-activedescendant`. The roles are
 * applied only while the list is open — a textarea permanently announced
 * as a combobox would mislead every author who never types an `@`.
 *
 * It closes on anything that says the `@` was prose: a space, a caret
 * moved elsewhere, a query no member matches. In each case the text the
 * author typed is left exactly as it was.
 *
 * The list is anchored at the caret, which is not an element, so it is
 * positioned in viewport coordinates rather than by a floating anchor.
 */

import VisuallyHidden from '@nodate-flow/ui/a11y/visually-hidden';
import Avatar from '@nodate-flow/ui/primitives/avatar';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import {
  type ChangeEvent,
  type KeyboardEvent,
  type ReactElement,
  type RefObject,
  useEffect,
  useId,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { filterCandidates, type MentionCandidate, useMentionCandidatesQuery } from './api';
import { type CaretRect, caretRect } from './caret-rect';
import styles from './mention-picker.module.css';
import { findMentionQuery, insertMention, type MentionQuery } from './notation';

/** Gap between the caret line and the popup, in CSS pixels. */
const CARET_GAP = 4;

/** Room the popup needs below the caret before it flips above it. */
const POPUP_RESERVE = 240;

export interface MentionTextareaProps {
  /** Current body text. */
  value: string;
  /** Called with the next body text. */
  onChange: (next: string) => void;
  /**
   * Workspace whose members can be named. Leave undefined where there is
   * no workspace in scope; the field is then an ordinary textarea.
   */
  workspaceId?: string | undefined;
  /** Ref to the underlying textarea, shared with the caller's toolbar. */
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  rows?: number | undefined;
  autoFocus?: boolean | undefined;
  /** Already-translated placeholder. */
  placeholder?: string | undefined;
  id?: string | undefined;
  /** Already-translated accessible label. */
  'aria-label'?: string | undefined;
  'aria-describedby'?: string | undefined;
  'aria-invalid'?: boolean | undefined;
  /** Runs for every key the picker did not consume. */
  onKeyDown?: ((event: KeyboardEvent<HTMLTextAreaElement>) => void) | undefined;
}

/** Up to two letters standing in for a member with no avatar. */
function initialsFrom(name: string): string {
  const parts = name.trim().split(' ').filter(Boolean);
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? '') : '';
  return (first + last).toUpperCase() || '?';
}

export default function MentionTextarea({
  value,
  onChange,
  workspaceId,
  textareaRef,
  rows,
  autoFocus,
  placeholder,
  id,
  'aria-label': ariaLabel,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
  onKeyDown,
}: MentionTextareaProps): ReactElement {
  const { t } = useTranslation('common');
  const enabled = workspaceId !== undefined && workspaceId.length > 0;

  const [mention, setMention] = useState<MentionQuery | null>(null);
  const [anchor, setAnchor] = useState<CaretRect | null>(null);
  const [activeRow, setActiveRow] = useState(0);

  const candidates = useMentionCandidatesQuery(workspaceId, mention !== null);
  const items = mention === null ? [] : filterCandidates(candidates.data ?? [], mention.query);
  const activeIndex = Math.min(activeRow, Math.max(0, items.length - 1));

  // Only a failure that leaves us with nothing is worth showing. A
  // background refetch that fails over a list we already hold would
  // otherwise replace the people on screen with an error the author can
  // do nothing about.
  const failed = candidates.isError && candidates.data === undefined;
  const loading = !failed && candidates.data === undefined;
  // A query nobody matches closes the list rather than showing an empty
  // one: an `@` the author meant literally should leave no popup sitting
  // over the text they are still writing.
  const open = mention !== null && anchor !== null && (items.length > 0 || loading || failed);

  const baseId = useId();
  const listId = `${baseId}-list`;
  const optionId = (index: number): string => `${baseId}-opt-${String(index)}`;

  const close = (): void => {
    setMention(null);
    setAnchor(null);
    setActiveRow(0);
  };

  /** Recompute what the caret is asking for after a text or caret change. */
  const sync = (nextValue: string, caret: number): void => {
    if (!enabled) return;
    const element = textareaRef.current;
    if (!element) return;
    const found = findMentionQuery(nextValue, caret);
    if (found === null) {
      close();
      return;
    }
    setMention(found);
    setActiveRow(0);
    if (mention === null || mention.start !== found.start) {
      setAnchor(caretRect(element, found.start));
    }
  };

  /**
   * Keep the popup on the caret while the page moves under it. Scroll and
   * resize are the two ways the anchor can go stale without the textarea
   * being touched, and a popup left behind points at the wrong person.
   */
  const anchorIndex = mention?.start ?? null;
  useEffect(() => {
    if (anchorIndex === null) return;
    const element = textareaRef.current;
    if (!element) return;
    const reposition = (): void => {
      setAnchor(caretRect(element, anchorIndex));
    };
    window.addEventListener('scroll', reposition, true);
    window.addEventListener('resize', reposition);
    return () => {
      window.removeEventListener('scroll', reposition, true);
      window.removeEventListener('resize', reposition);
    };
  }, [anchorIndex, textareaRef]);

  const choose = (candidate: MentionCandidate): void => {
    const element = textareaRef.current;
    if (!element || mention === null) return;
    const result = insertMention(
      value,
      mention.start,
      element.selectionStart,
      candidate.displayName,
      candidate.userId,
    );
    onChange(result.value);
    close();
    // The caret can only be placed once React has written the new value
    // back into the textarea.
    requestAnimationFrame(() => {
      element.focus();
      element.setSelectionRange(result.caret, result.caret);
    });
  };

  const handleChange = (event: ChangeEvent<HTMLTextAreaElement>): void => {
    onChange(event.target.value);
    sync(event.target.value, event.target.selectionStart);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    // While an input method owns the keystroke, Enter commits the
    // composition and the arrows cycle its candidates. Acting on them
    // would pick a member on the Enter that finishes a kana word.
    const composing = event.nativeEvent.isComposing || event.keyCode === 229;
    if (open && !composing) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setActiveRow(items.length === 0 ? 0 : (activeIndex + 1) % items.length);
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setActiveRow(items.length === 0 ? 0 : (activeIndex - 1 + items.length) % items.length);
        return;
      }
      if (event.key === 'Enter') {
        const candidate = items[activeIndex];
        if (candidate) {
          event.preventDefault();
          choose(candidate);
          return;
        }
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        // The overlay stack listens for Escape on `document`; without
        // this the same key would dismiss the picker and the dialog
        // holding the editor, losing whatever was typed into it.
        event.stopPropagation();
        close();
        return;
      }
      if (
        event.key === 'ArrowLeft' ||
        event.key === 'ArrowRight' ||
        event.key === 'Home' ||
        event.key === 'End'
      ) {
        // The caret is leaving the query. Let the key through and stop
        // offering people for text it is no longer inside.
        close();
      }
    }
    onKeyDown?.(event);
  };

  /**
   * What the picker is doing, for anyone who cannot see it. Announced
   * while a mention is being typed rather than only while the list is
   * open, so a query that matches nobody — which closes the list — says
   * so instead of going quiet.
   */
  const status =
    mention === null
      ? ''
      : failed
        ? t('tasks.mentions.load_failed')
        : loading
          ? t('tasks.mentions.loading')
          : t('tasks.mentions.results', { count: items.length });

  const showRows = open && items.length > 0;

  return (
    <>
      <Textarea
        ref={textareaRef}
        id={id}
        value={value}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        onBlur={close}
        onClick={close}
        rows={rows}
        autoFocus={autoFocus}
        placeholder={placeholder}
        aria-label={ariaLabel}
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid}
        role={open ? 'combobox' : undefined}
        aria-expanded={open ? true : undefined}
        aria-controls={open ? listId : undefined}
        aria-autocomplete={open ? 'list' : undefined}
        aria-activedescendant={showRows ? optionId(activeIndex) : undefined}
      />
      {open && anchor !== null ? (
        <ul
          id={listId}
          role="listbox"
          aria-label={t('tasks.mentions.listbox_label')}
          className={styles.list}
          style={popupPosition(anchor)}
        >
          {/*
           * A listbox may only own options, so the two rows that are not
           * people are presentational. Their text still reaches assistive
           * technology through the live region above.
           */}
          {failed ? (
            <li role="presentation" className={styles.statusRow}>
              {t('tasks.mentions.load_failed')}
            </li>
          ) : null}
          {!failed && loading ? (
            <li role="presentation" className={styles.statusRow}>
              {t('tasks.mentions.loading')}
            </li>
          ) : null}
          {showRows
            ? items.map((candidate, index) => (
                <li
                  key={candidate.userId}
                  id={optionId(index)}
                  role="option"
                  aria-selected={index === activeIndex}
                  className={`${styles.option} ${index === activeIndex ? styles.optionActive : ''}`}
                  onMouseDown={(event) => {
                    // Keep focus — and therefore the caret — in the
                    // textarea we are about to write into.
                    event.preventDefault();
                    choose(candidate);
                  }}
                >
                  <MemberAvatar candidate={candidate} />
                  <span className={styles.names}>
                    <span className={styles.displayName}>{candidate.displayName}</span>
                    <span className={styles.email}>{candidate.email}</span>
                  </span>
                </li>
              ))
            : null}
        </ul>
      ) : null}
      {enabled ? (
        <VisuallyHidden>
          <span role="status">{status}</span>
        </VisuallyHidden>
      ) : null}
    </>
  );
}

/** Avatar image when the member has one, initials otherwise. */
function MemberAvatar({ candidate }: { candidate: MentionCandidate }): ReactElement {
  return (
    <Avatar
      alt={candidate.displayName}
      initials={initialsFrom(candidate.displayName)}
      size="sm"
      {...(candidate.avatarUrl ? { src: candidate.avatarUrl } : {})}
    />
  );
}

/**
 * Place the popup under the caret line, flipping above it when the
 * viewport has no room below. Horizontal placement is written as a
 * logical inset so the popup stays on the caret's side of the field in a
 * right-to-left body, where the measured pixel offset counts from the
 * other edge.
 */
function popupPosition(anchor: CaretRect): {
  insetInlineStart: string;
  insetBlockStart?: string;
  insetBlockEnd?: string;
} {
  const rtl = document.documentElement.dir === 'rtl';
  const inlineStart = rtl ? window.innerWidth - anchor.left : anchor.left;
  const below = window.innerHeight - anchor.bottom;
  const flip = below < POPUP_RESERVE && anchor.top > below;
  return {
    insetInlineStart: `${String(Math.max(0, inlineStart))}px`,
    ...(flip
      ? { insetBlockEnd: `${String(Math.max(0, window.innerHeight - anchor.top + CARET_GAP))}px` }
      : { insetBlockStart: `${String(anchor.bottom + CARET_GAP)}px` }),
  };
}
