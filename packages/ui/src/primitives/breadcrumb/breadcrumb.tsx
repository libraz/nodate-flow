/**
 * Breadcrumb — hierarchical navigation trail primitive.
 *
 * Renders a semantic `<nav aria-label>` wrapping an `<ol>` of
 * {@link BreadcrumbItem} and {@link BreadcrumbSeparator} children. The last
 * {@link BreadcrumbItem} that has no `href` is automatically marked with
 * `aria-current="page"`; callers may force this with `current={true}`.
 *
 * Visual rhythm (font size, muted link color, inline separator) is extracted
 * from the two ad-hoc breadcrumb implementations in flow-web
 * (`features/pages/page-detail.tsx` and the task detail route) so that
 * migrating those call sites to this primitive is a zero-visual-diff change.
 *
 * The color convention matches the sidebar (flow-web) and the task-detail
 * breadcrumb: links are `--nf-color-fg-muted` by default and resolve to
 * `--nf-color-fg` on hover / focus. The current item uses `--nf-color-fg`
 * with medium weight.
 *
 * For TanStack Router integration, wrap a `<Link>` using `asChild`:
 *
 * ```tsx
 * <BreadcrumbItem asChild>
 *   <Link to="/workspaces/$id" params={{ id: wsId }}>{wsName}</Link>
 * </BreadcrumbItem>
 * ```
 *
 * When `asChild` is true the primitive clones the single child, merging the
 * item className so the consumer's `<Link>` inherits the breadcrumb link
 * styling while retaining its own props / ref.
 */

import {
  type AnchorHTMLAttributes,
  Children,
  cloneElement,
  forwardRef,
  type HTMLAttributes,
  isValidElement,
  type ReactElement,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react';
import { cx } from '../../lib/cx';
import styles from './breadcrumb.module.css';

/** Props for {@link Breadcrumb}. */
export interface BreadcrumbProps extends Omit<HTMLAttributes<HTMLElement>, 'aria-label'> {
  /** Accessible label for the surrounding nav. Defaults to `'breadcrumb'`. */
  label?: string;
  /** Breadcrumb trail — alternating {@link BreadcrumbItem} / {@link BreadcrumbSeparator}. */
  children: ReactNode;
}

/** Props for {@link BreadcrumbItem}. */
export interface BreadcrumbItemProps {
  /** If provided, the item renders as a link to this URL. */
  href?: string;
  /**
   * Force `aria-current="page"` on this item. Without this flag, the last
   * {@link BreadcrumbItem} that has no `href` is implicitly treated as the
   * current page (see {@link Breadcrumb}'s wrapper logic).
   */
  current?: boolean;
  /** Visible item content. */
  children: ReactNode;
  /** Optional click handler, mainly for router-link call sites. */
  onClick?: (event: ReactMouseEvent) => void;
  /** Optional className forwarded to the rendered element. */
  className?: string;
  /**
   * When `true`, the primitive expects a single React element child (e.g.
   * a router `<Link>`) and clones it so the link picks up breadcrumb styling
   * while retaining its own props / ref.
   */
  asChild?: boolean;
}

/** Props for {@link BreadcrumbSeparator}. */
export interface BreadcrumbSeparatorProps {
  /** Override the separator glyph. Defaults to U+203A (`›`). */
  children?: ReactNode;
  /** Optional className forwarded to the `<li>` wrapper. */
  className?: string;
}

/** Internal marker used by {@link Breadcrumb} to detect separators vs items. */
const SEPARATOR_KIND = 'BreadcrumbSeparator';
/** Internal marker used by {@link Breadcrumb} to detect item children. */
const ITEM_KIND = 'BreadcrumbItem';

/** Internal shape for rendered item props (used for type-narrowing during clone). */
type AnchorProps = AnchorHTMLAttributes<HTMLAnchorElement> & { className?: string };

/**
 * Breadcrumb renders a semantic nav + ordered list wrapper. The last
 * non-href item is auto-tagged as `aria-current="page"` so consumers don't
 * need to remember to set it manually.
 */
const Breadcrumb = forwardRef<HTMLElement, BreadcrumbProps>(
  ({ label, children, className, ...rest }, ref): ReactElement => {
    /*
     * Walk children once to locate the last BreadcrumbItem so we can inject
     * `current={true}` when the consumer did not specify `href` on it and
     * did not already set `current`. This preserves the documented API (the
     * last item without href is the current page) without forcing consumers
     * to be verbose.
     */
    const list = Children.toArray(children).filter(isValidElement);
    let lastItemIndex = -1;
    for (let i = list.length - 1; i >= 0; i -= 1) {
      const node = list[i];
      if (node != null && getKind(node) === ITEM_KIND) {
        lastItemIndex = i;
        break;
      }
    }

    const rendered = list.map((child, index) => {
      if (index !== lastItemIndex) return child;
      const itemProps = child.props as BreadcrumbItemProps;
      if (itemProps.current === true || itemProps.href != null) return child;
      return cloneElement(child as ReactElement<BreadcrumbItemProps>, { current: true });
    });

    return (
      <nav
        ref={ref}
        aria-label={label ?? 'breadcrumb'}
        className={cx(styles.root, className)}
        {...rest}
      >
        <ol className={styles.list}>{rendered}</ol>
      </nav>
    );
  },
);
Breadcrumb.displayName = 'Breadcrumb';

/**
 * BreadcrumbItem renders a single step of the trail as an `<li>` containing
 * either an `<a>` (when `href` is supplied), a router `<Link>` substitute
 * (when `asChild` is used), or a `<span aria-current="page">` for the
 * terminal / current item.
 */
const BreadcrumbItem = forwardRef<HTMLLIElement, BreadcrumbItemProps>(
  ({ href, current, children, onClick, className, asChild }, ref): ReactElement => {
    const ariaCurrent = current === true ? ('page' as const) : undefined;

    if (asChild === true) {
      /*
       * Slot-style asChild: expect exactly one valid React element and
       * merge wrapper className / onClick / aria-current onto it. Fall back
       * gracefully to rendering children as-is if the consumer passed
       * something invalid — matches the tooltip primitive's behaviour.
       */
      const only = Children.only(children);
      if (isValidElement(only)) {
        const childProps = only.props as AnchorProps;
        const merged: AnchorProps = {
          ...childProps,
          className: cx(styles.link, childProps.className),
          onClick: (event) => {
            childProps.onClick?.(event);
            onClick?.(event);
          },
          'aria-current': ariaCurrent ?? childProps['aria-current'],
        };
        return (
          <li ref={ref} className={cx(styles.item, className)}>
            {cloneElement(only, merged)}
          </li>
        );
      }
      return (
        <li ref={ref} className={cx(styles.item, className)}>
          {children}
        </li>
      );
    }

    if (href != null && current !== true) {
      return (
        <li ref={ref} className={cx(styles.item, className)}>
          <a href={href} onClick={onClick} className={styles.link}>
            {children}
          </a>
        </li>
      );
    }

    return (
      <li ref={ref} className={cx(styles.item, className)}>
        <span aria-current={ariaCurrent} className={styles.current}>
          {children}
        </span>
      </li>
    );
  },
);
BreadcrumbItem.displayName = 'BreadcrumbItem';
(BreadcrumbItem as unknown as { __breadcrumbKind?: string }).__breadcrumbKind = ITEM_KIND;

/**
 * BreadcrumbSeparator renders an `<li aria-hidden>` containing the separator
 * glyph. Defaults to U+203A (single right-pointing angle quotation mark).
 */
const BreadcrumbSeparator = forwardRef<HTMLLIElement, BreadcrumbSeparatorProps>(
  ({ children, className }, ref): ReactElement => (
    <li ref={ref} aria-hidden="true" className={cx(styles.separator, className)}>
      {children ?? '›'}
    </li>
  ),
);
BreadcrumbSeparator.displayName = 'BreadcrumbSeparator';
(BreadcrumbSeparator as unknown as { __breadcrumbKind?: string }).__breadcrumbKind = SEPARATOR_KIND;

/**
 * Determine whether a React element is a BreadcrumbItem / BreadcrumbSeparator
 * by reading the private `__breadcrumbKind` field stamped above. This avoids
 * using `type === BreadcrumbItem` which breaks when consumers forward the
 * component through wrappers.
 */
function getKind(element: ReactElement): string | undefined {
  const type = element.type as { __breadcrumbKind?: string } | undefined;
  return type?.__breadcrumbKind;
}

export { Breadcrumb, BreadcrumbItem, BreadcrumbSeparator };
export default Breadcrumb;
