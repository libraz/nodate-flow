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
 */
export const NOTIFICATION_EVENT_KEY: Readonly<Record<string, string>> = {
  'task.created': 'event.task_created',
  'task.updated': 'event.task_updated',
  'task.disabled': 'event.task_disabled',
  'task.comment.added': 'event.task_comment_added',
  'task.comment.edited': 'event.task_comment_edited',
  'task.comment.removed': 'event.task_comment_removed',
  'task.actor.added': 'event.task_actor_added',
  'task.actor.removed': 'event.task_actor_removed',
  'task.transition.start': 'event.task_transition_start',
  'task.transition.complete': 'event.task_transition_complete',
  'task.transition.block': 'event.task_transition_block',
  'task.transition.unblock': 'event.task_transition_unblock',
  'task.transition.submit': 'event.task_transition_submit',
  'task.transition.reopen': 'event.task_transition_reopen',
  'task.transition.cancel': 'event.task_transition_cancel',
  'item.scheduled': 'event.item_scheduled',
  'item.unscheduled': 'event.item_unscheduled',
  'item.rescheduled': 'event.item_rescheduled',
  'item.renamed': 'event.item_renamed',
  'item.deleted': 'event.item_deleted',
  'item.reconciled': 'event.item_reconciled',
  'item.actor.added': 'event.item_actor_added',
  'item.actor.removed': 'event.item_actor_removed',
  'item.visibility.changed': 'event.item_visibility_changed',
  'item.milestone.link.added': 'event.item_milestone_link_added',
  'item.milestone.link.removed': 'event.item_milestone_link_removed',
  'agent.task.handoff_to_user': 'event.agent_task_handoff_to_user',
  'agent.task.handoff_to_agent': 'event.agent_task_handoff_to_agent',
  'agent.task.attached': 'event.agent_task_attached',
  'agent.task.detached': 'event.agent_task_detached',
};
