/**
 * MarkdownEditor — textarea with a formatting toolbar and live preview
 * toggle for Markdown content. Used in task description editing.
 *
 * This is NOT a WYSIWYG editor. It manipulates raw Markdown text via
 * toolbar buttons that insert syntax around the current selection.
 * Preview mode renders the Markdown using the shared `<Markdown>`
 * component (react-markdown + remark-gfm).
 */

import Button from '@nodate-flow/ui/primitives/button';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { type ChangeEvent, type ReactElement, type RefObject, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import Markdown from '@nodate-flow/ui/primitives/markdown';

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
}: MarkdownEditorProps): ReactElement {
  const { t } = useTranslation('common');
  const [previewing, setPreviewing] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const handleChange = (e: ChangeEvent<HTMLTextAreaElement>): void => {
    onChange(e.target.value);
  };

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

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
      {/* Toolbar */}
      <div
        role="toolbar"
        aria-label={t('tasks.markdown_editor.toolbar')}
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: '0.25rem',
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
          style={{ fontWeight: 700, minInlineSize: '2rem' }}
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
          style={{ fontStyle: 'italic', minInlineSize: '2rem' }}
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
          style={{ minInlineSize: '2rem' }}
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
          style={{ minInlineSize: '2rem' }}
        >
          &#8226;
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleCode}
          aria-label={t('tasks.markdown_editor.code')}
          disabled={previewing}
          style={{ fontFamily: 'var(--font-mono)', minInlineSize: '2rem' }}
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
          style={{ minInlineSize: '2rem' }}
        >
          &#128279;
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleQuote}
          aria-label={t('tasks.markdown_editor.quote')}
          disabled={previewing}
          style={{ minInlineSize: '2rem' }}
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
            borderRadius: '0.375rem',
            padding: '0.75rem',
            minBlockSize: `${rows * 1.5}rem`,
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
        <Textarea
          ref={textareaRef}
          value={value}
          onChange={handleChange}
          rows={rows}
          autoFocus={autoFocus}
          aria-label={ariaLabel}
        />
      )}
    </div>
  );
}
