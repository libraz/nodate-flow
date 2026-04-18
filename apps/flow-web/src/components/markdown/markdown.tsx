/**
 * Markdown — read-only preview renderer used for task descriptions and
 * comments. Intentionally minimal: GFM (tables, task lists, strikethrough,
 * autolinks) is enabled but raw HTML is NOT — `react-markdown` defaults
 * escape HTML so this is XSS-safe by construction.
 *
 * Editing UX remains a plain textarea (Phase 2 will upgrade that).
 */

import { type ReactElement, memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import styles from './markdown.module.css';

interface MarkdownProps {
  /** Raw markdown source. */
  children: string;
  /** Optional extra class on the wrapper. */
  className?: string;
}

function MarkdownImpl({ children, className }: MarkdownProps): ReactElement {
  return (
    <div className={className ? `${styles.md} ${className}` : styles.md}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Force external links to open safely.
          a: ({ href, children: body, ...rest }) => (
            <a href={href} target="_blank" rel="noreferrer noopener" {...rest}>
              {body}
            </a>
          ),
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}

export const Markdown = memo(MarkdownImpl);
export default Markdown;
