/**
 * Markdown — read-only GFM preview renderer.
 *
 * Renders GitHub-Flavored Markdown (tables, task lists, strikethrough,
 * autolinks) as styled HTML. Raw HTML is NOT allowed — `react-markdown`
 * escapes it by default, so this component is XSS-safe by construction.
 *
 * External links open in a new tab with `rel="noreferrer noopener"`.
 *
 * @example
 * ```tsx
 * import Markdown from '@nodate-flow/ui/primitives/markdown';
 * <Markdown>{description}</Markdown>
 * ```
 */

import { memo, type ReactElement } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { cx } from '../../lib/cx';
import styles from './markdown.module.css';

export interface MarkdownProps {
  /** Raw markdown source. */
  children: string;
  /** Optional extra class on the wrapper. */
  className?: string;
}

function MarkdownImpl({ children, className }: MarkdownProps): ReactElement {
  return (
    <div className={cx(styles.md, className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
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

/** Markdown renders GFM source as styled, read-only HTML. */
const Markdown = memo(MarkdownImpl);
Markdown.displayName = 'Markdown';

export default Markdown;
