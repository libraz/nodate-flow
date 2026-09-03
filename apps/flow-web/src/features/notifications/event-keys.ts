/**
 * Notification event type to i18n key.
 *
 * The server stores a pre-rendered English title on every notification
 * row and the dropdown used to print it verbatim, so a reader in Japanese
 * or Chinese got an English product with English notifications inside it.
 * Nothing caught it: the string never passes through `t()`, so the i18n
 * checks see no key, and it is not a literal in this codebase either, so
 * the hardcoded-string checks see nothing.
 *
 * No migration was needed. Every row already carries `eventType` —
 * NOT NULL, indexed — which is the real key; the stored title was
 * redundant from the start. Existing notifications translate along with
 * new ones.
 *
 * A static map rather than `t(\`event.${type}\`)`: dynamic keys are
 * banned because nothing can then tell which keys are reachable, and a
 * missing one only shows up as a raw key in front of a reader.
 *
 * The pairs live in `event-keys.json` rather than in this file so the
 * server can read them. The set of event types the server notifies on is
 * decided in Go, and the two lists have to agree; a Go test loads this
 * JSON and compares it against the fan-out's classification table, which
 * only works if the data is parseable rather than embedded in a module.
 */

import eventKeys from './event-keys.json';

export const NOTIFICATION_EVENT_KEY: Readonly<Record<string, string>> = eventKeys;
