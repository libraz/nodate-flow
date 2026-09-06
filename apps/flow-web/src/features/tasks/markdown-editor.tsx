/**
 * MarkdownEditor — textarea with a formatting toolbar and live preview
 * toggle for Markdown content. Used everywhere a body is written: task
 * descriptions, task comments, wiki pages.
 *
 * This is NOT a WYSIWYG editor. It manipulates raw Markdown text via
 * toolbar buttons that insert syntax around the current selection.
 * Preview mode renders the Markdown using the shared `<Markdown>`
 * component (react-markdown + remark-gfm).
 *
 * Given a `workspaceId` the textarea also offers the workspace's people
 * when the author types `@`. Every body that goes through this component
 * gets the picker from the one place, which is why the comment box uses
 * the editor rather than a bare textarea of its own.
 */

import Button from '@nodate-flow/ui/primitives/button';
import Markdown from '@nodate-flow/ui/primitives/markdown';
import { type KeyboardEvent, type ReactElement, type RefObject, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import MentionTextarea from '../mentions/mention-textarea';

interface MarkdownEditorProps {
  /** Current Markdown source text. */
  value: string;
  /** Called when the user edits the source text. */
  onChange: (next: string) => void;
  /** Minimum visible rows for the textarea. */
  rows?: number;
  /** Accessible label forwarded to the textarea. */
  'aria-label'?: string;
  /** Whether to auto-focus the textarea on mount. */
  autoFocus?: boolean;
  /**
   * Workspace whose members can be named with `@`. Omit where there is
   * no workspace in scope and the mention picker stays off.
   */
  workspaceId?: string | undefined;
  /** Id for the textarea, so a `<FormField>` label can point at it. */
  id?: string | undefined;
  /** Already-translated placeholder for the textarea. */
  placeholder?: string | undefined;
  /** Runs for every key the mention picker did not consume. */
  onKeyDown?: ((event: KeyboardEvent<HTMLTextAreaElement>) => void) | undefined;
}

/** Wrap the selected text (or insert at cursor) with `before` / `after`. */
function wrapSelection(
  ref: RefObject<HTMLTextAreaElement | null>,
  value: string,
  before: string,
  after: string,
  onChange: (next: string) => void,
): void {
  const el = ref.current;
  if (!el) return;

  const start = el.selectionStart;
  const end = el.selectionEnd;
  const selected = value.slice(start, end);
  const replacement = `${before}${selected}${after}`;
  const next = `${value.slice(0, start)}${replacement}${value.slice(end)}`;
  onChange(next);

  // Restore cursor position after React re-render.
  requestAnimationFrame(() => {
    el.focus();
    const cursorPos = selected.length > 0 ? start + replacement.length : start + before.length;
    el.setSelectionRange(cursorPos, cursorPos);
  });
}

/** Insert a prefix at the start of the current line. */
function prefixLine(
  ref: RefObject<HTMLTextAreaElement | null>,
  value: string,
  prefix: string,
  onChange: (next: string) => void,
): void {
  const el = ref.current;
  if (!el) return;

  const start = el.selectionStart;
  const lineStart = value.lastIndexOf('\n', start - 1) + 1;
  const next = `${value.slice(0, lineStart)}${prefix}${value.slice(lineStart)}`;
  onChange(next);

  requestAnimationFrame(() => {
    el.focus();
    const cursorPos = start + prefix.length;
    el.setSelectionRange(cursorPos, cursorPos);
  });
}

/**
 * MarkdownEditor renders a toolbar + textarea pair with an optional
 * preview pane. All toolbar labels are i18n keys under
 * `tasks.markdown_editor.*`.
 */
export default function MarkdownEditor({
  value,
  onChange,
  rows = 6,
  'aria-label': ariaLabel,
  autoFocus,
  workspaceId,
  id,
  placeholder,
  onKeyDown,
}: MarkdownEditorProps): ReactElement {
  const { t } = useTranslation('common');
  const [previewing, setPreviewing] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const handleBold = (): void => {
    wrapSelection(textareaRef, value, '**', '**', onChange);
  };

  const handleItalic = (): void => {
    wrapSelection(textareaRef, value, '_', '_', onChange);
  };

  const handleHeading = (): void => {
    prefixLine(textareaRef, value, '## ', onChange);
  };

  const handleList = (): void => {
    prefixLine(textareaRef, value, '- ', onChange);
  };

  const handleCode = (): void => {
    wrapSelection(textareaRef, value, '`', '`', onChange);
  };

  const handleLink = (): void => {
    wrapSelection(textareaRef, value, '[', '](url)', onChange);
  };

  const handleQuote = (): void => {
    prefixLine(textareaRef, value, '> ', onChange);
  };

  // Square icon-button toolbar items — `aspect-ratio: 1` paired with the
  // `sm` button vertical padding lets each glyph share an equal hit-target
  // without freezing a literal pixel/rem width.
  const toolbarButtonStyle = { aspectRatio: '1', padding: 'var(--nf-space-1)' } as const;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-2)' }}>
      {/* Toolbar */}
      <div
        role="toolbar"
        aria-label={t('tasks.markdown_editor.toolbar')}
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 'var(--nf-space-1)',
          alignItems: 'center',
        }}
      >
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleBold}
          aria-label={t('tasks.markdown_editor.bold')}
          disabled={previewing}
          style={{ ...toolbarButtonStyle, fontWeight: 700 }}
        >
          B
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleItalic}
          aria-label={t('tasks.markdown_editor.italic')}
          disabled={previewing}
          style={{ ...toolbarButtonStyle, fontStyle: 'italic' }}
        >
          I
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleHeading}
          aria-label={t('tasks.markdown_editor.heading')}
          disabled={previewing}
          style={toolbarButtonStyle}
        >
          H
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleList}
          aria-label={t('tasks.markdown_editor.list')}
          disabled={previewing}
          style={toolbarButtonStyle}
        >
          •
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleCode}
          aria-label={t('tasks.markdown_editor.code')}
          disabled={previewing}
          style={{ ...toolbarButtonStyle, fontFamily: 'var(--nf-font-mono)' }}
        >
          {'</>'}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleLink}
          aria-label={t('tasks.markdown_editor.link')}
          disabled={previewing}
          style={toolbarButtonStyle}
        >
          🔗
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleQuote}
          aria-label={t('tasks.markdown_editor.quote')}
          disabled={previewing}
          style={toolbarButtonStyle}
        >
          &gt;
        </Button>

        {/* Spacer */}
        <span style={{ flex: 1 }} />

        {/* Preview toggle */}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => {
            setPreviewing((prev) => !prev);
          }}
          aria-pressed={previewing}
        >
          {previewing ? t('tasks.markdown_editor.write') : t('tasks.markdown_editor.preview')}
        </Button>
      </div>

      {/* Editor / Preview area */}
      {previewing ? (
        <div
          style={{
            border: '1px solid var(--nf-color-border)',
            borderRadius: 'var(--nf-radius-sm)',
            padding: 'var(--nf-space-3)',
            // `--nf-space-6` is 1.5rem so this preserves the original
            // `${rows} * 1.5rem` minimum height while routing the base
            // unit through the spacing scale.
            minBlockSize: `calc(var(--nf-space-6) * ${String(rows)})`,
            background: 'var(--nf-color-surface)',
          }}
        >
          {value.trim().length > 0 ? (
            <Markdown>{value}</Markdown>
          ) : (
            <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)' }}>
              {t('tasks.markdown_editor.preview_empty')}
            </p>
          )}
        </div>
      ) : (
        <MentionTextarea
          textareaRef={textareaRef}
          value={value}
          onChange={onChange}
          workspaceId={workspaceId}
          id={id}
          rows={rows}
          autoFocus={autoFocus}
          placeholder={placeholder}
          aria-label={ariaLabel}
          onKeyDown={onKeyDown}
        />
      )}
    </div>
  );
}
