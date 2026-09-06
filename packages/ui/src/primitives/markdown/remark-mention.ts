/**
 * remarkMention — recognise the mention notation inside a body.
 *
 * A mention is written as an `@` immediately followed by a markdown link
 * carrying a user id:
 *
 *     @[Display Name](user:019649b0-0000-7000-8000-000000000000)
 *
 * Markdown parses that as the text `@` next to an ordinary link, so this
 * plugin does two things: it drops the `@` from the text that precedes
 * the link (the renderer draws its own), and it moves the link onto a
 * private scheme so the renderer can tell a mention from any other link
 * that happens to point at a `user:` URL.
 *
 * The `@` is what makes it a mention. A `[name](user:id)` link with no
 * `@` in front of it is left alone, because the backend does not read one
 * as a mention either — and a chip drawn for something nobody was
 * notified about would be a lie about what the body did.
 */

import type { Nodes, Parents, Root } from 'mdast';

/** Scheme the author's notation carries. */
const USER_SCHEME = 'user:';

/**
 * Scheme a recognised mention is rewritten onto. Private to this pair of
 * files: it never appears in a stored body, and the renderer treats it as
 * the sole marker of a mention.
 */
export const MENTION_SCHEME = 'nf-mention:';

function isParent(node: Nodes): node is Parents {
  return 'children' in node;
}

function transform(parent: Parents): void {
  const children = parent.children;
  for (let index = 0; index < children.length; index += 1) {
    const node = children[index];
    if (node === undefined) continue;

    if (node.type === 'link' && node.url.startsWith(USER_SCHEME)) {
      const previous = index > 0 ? children[index - 1] : undefined;
      if (previous !== undefined && previous.type === 'text' && previous.value.endsWith('@')) {
        previous.value = previous.value.slice(0, -1);
        node.url = MENTION_SCHEME + node.url.slice(USER_SCHEME.length);
      }
    }

    if (isParent(node)) transform(node);
  }
}

/** Unified plugin recognising mention notation in an mdast tree. */
export function remarkMention(): (tree: Root) => void {
  return (tree: Root): void => {
    transform(tree);
  };
}
