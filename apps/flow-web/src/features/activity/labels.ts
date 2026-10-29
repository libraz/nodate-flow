/**
 * Activity-feed label helpers — turn raw backend enums (`source`,
 * `severity`, `actorKind`) and dotted `action` codes into localized,
 * human-readable strings.
 *
 * Every key is a literal in a static map, so the catalog is greppable and
 * a code outside it never becomes a key. Dotted action codes are
 * normalized to an underscore slug before the lookup; anything outside the
 * catalog falls back to a humanized form of the raw code so newly-added
 * backend actions still read sensibly instead of rendering nothing.
 */

import type { TFunction } from 'i18next';

import type { ActivityActorKind, ActivitySeverity, ActivitySource } from './api';

/**
 * Normalize a dotted action code (e.g. `task.create`,
 * `calendar.event.update`) into the underscore slug used as the i18n key
 * suffix (`task_create`, `calendar_event_update`).
 */
export function actionSlug(action: string): string {
  return action.replace(/\./g, '_');
}

/**
 * Humanize a raw dotted/underscored action code into a readable phrase as
 * a last resort when no catalog entry exists. `task.create` ->
 * "Task create"; `calendar.event.update` -> "Calendar event update".
 */
export function humanizeAction(action: string): string {
  const words = action.split(/[._]/).filter((part) => part.length > 0);
  if (words.length === 0) return action;
  const [head, ...rest] = words;
  const lead = (head ?? '').charAt(0).toUpperCase() + (head ?? '').slice(1);
  return [lead, ...rest].join(' ');
}

const ACTION_KEY: ReadonlyMap<string, string> = new Map([
  ['ai_agent_create', 'action.ai_agent_create'],
  ['ai_agent_pause', 'action.ai_agent_pause'],
  ['ai_agent_update_schedule', 'action.ai_agent_update_schedule'],
  ['ai_agent_update_triggers', 'action.ai_agent_update_triggers'],
  ['ai_command_resolve', 'action.ai_command_resolve'],
  ['ai_invocation', 'action.ai_invocation'],
  ['ai_provider_create', 'action.ai_provider_create'],
  ['ai_provider_delete', 'action.ai_provider_delete'],
  ['ai_provider_update', 'action.ai_provider_update'],
  ['attachment_create', 'action.attachment_create'],
  ['attachment_delete', 'action.attachment_delete'],
  ['auth_login', 'action.auth_login'],
  ['auth_login_failed', 'action.auth_login_failed'],
  ['auth_login_oidc', 'action.auth_login_oidc'],
  ['auth_login_totp', 'action.auth_login_totp'],
  ['auth_login_totp_failed', 'action.auth_login_totp_failed'],
  ['auth_logout', 'action.auth_logout'],
  ['auth_recovery_code_used', 'action.auth_recovery_code_used'],
  ['calendar_event_create', 'action.calendar_event_create'],
  ['calendar_event_update', 'action.calendar_event_update'],
  ['calendar_event_delete', 'action.calendar_event_delete'],
  ['comment_create', 'action.comment_create'],
  ['comment_delete', 'action.comment_delete'],
  ['comment_update', 'action.comment_update'],
  ['description_version_restore', 'action.description_version_restore'],
  ['export_create', 'action.export_create'],
  ['favorite_create', 'action.favorite_create'],
  ['favorite_delete', 'action.favorite_delete'],
  ['import_cancel', 'action.import_cancel'],
  ['import_create', 'action.import_create'],
  ['intake_convert', 'action.intake_convert'],
  ['intake_create', 'action.intake_create'],
  ['intake_triage', 'action.intake_triage'],
  ['label_create', 'action.label_create'],
  ['label_disable', 'action.label_disable'],
  ['label_update', 'action.label_update'],
  ['lens_create', 'action.lens_create'],
  ['lens_delete', 'action.lens_delete'],
  ['lens_publish', 'action.lens_publish'],
  ['lens_unpublish', 'action.lens_unpublish'],
  ['lens_update', 'action.lens_update'],
  ['mcp_invocation', 'action.mcp_invocation'],
  ['mcp_token_create', 'action.mcp_token_create'],
  ['mcp_token_delete', 'action.mcp_token_delete'],
  ['notification_archive', 'action.notification_archive'],
  ['notification_read', 'action.notification_read'],
  ['page_create', 'action.page_create'],
  ['page_delete', 'action.page_delete'],
  ['page_update', 'action.page_update'],
  ['project_create', 'action.project_create'],
  ['project_delete', 'action.project_delete'],
  ['project_update', 'action.project_update'],
  ['reaction_create', 'action.reaction_create'],
  ['reaction_delete', 'action.reaction_delete'],
  ['signal_create', 'action.signal_create'],
  ['task_apply_steps', 'action.task_apply_steps'],
  ['task_archived', 'action.task_archived'],
  ['task_create', 'action.task_create'],
  ['task_delete', 'action.task_delete'],
  ['task_reorder', 'action.task_reorder'],
  ['task_smart_create', 'action.task_smart_create'],
  ['task_transition', 'action.task_transition'],
  ['task_unarchived', 'action.task_unarchived'],
  ['task_update', 'action.task_update'],
  ['timebox_create', 'action.timebox_create'],
  ['timebox_delete', 'action.timebox_delete'],
  ['timebox_status', 'action.timebox_status'],
  ['timebox_update', 'action.timebox_update'],
  ['user_update', 'action.user_update'],
  ['workspace_create', 'action.workspace_create'],
  ['workspace_delete', 'action.workspace_delete'],
  ['workspace_update', 'action.workspace_update'],
]);

/**
 * Resolve a localized label for a raw `action` code. Consults the
 * `activity:action.*` catalog and falls back to {@link humanizeAction} for
 * unknown codes so the row never renders an empty or broken label.
 */
export function actionLabel(action: string, t: TFunction): string {
  const key = ACTION_KEY.get(actionSlug(action));
  return key === undefined ? humanizeAction(action) : t(key);
}

const SOURCE_KEY: Readonly<Record<ActivitySource, string>> = {
  audit: 'source.audit',
  ai: 'source.ai',
  mcp: 'source.mcp',
};

function isKnownSource(value: string): value is ActivitySource {
  return Object.hasOwn(SOURCE_KEY, value);
}

/** Localized label for an originating stream (`audit` | `ai` | `mcp`). */
export function sourceLabel(source: string, t: TFunction): string {
  return isKnownSource(source) ? t(SOURCE_KEY[source]) : humanizeAction(source);
}

const ACTOR_KIND_KEY: Readonly<Record<ActivityActorKind, string>> = {
  user: 'actor_kind.user',
  agent: 'actor_kind.agent',
  system: 'actor_kind.system',
};

function isKnownKind(value: string): value is ActivityActorKind {
  return Object.hasOwn(ACTOR_KIND_KEY, value);
}

/** Localized label for an actor classification. */
export function actorKindLabel(actorKind: string, t: TFunction): string {
  return isKnownKind(actorKind) ? t(ACTOR_KIND_KEY[actorKind]) : humanizeAction(actorKind);
}

const SEVERITY_KEY: Readonly<Record<ActivitySeverity, string>> = {
  info: 'severity.info',
  warn: 'severity.warn',
  error: 'severity.error',
};

function isKnownSeverity(value: string): value is ActivitySeverity {
  return Object.hasOwn(SEVERITY_KEY, value);
}

/** Normalize a raw severity into the closed `ActivitySeverity` set. */
export function normalizeSeverity(severity: string): ActivitySeverity {
  return isKnownSeverity(severity) ? severity : 'info';
}

/** Localized label for a severity (`info` | `warn` | `error`). */
export function severityLabel(severity: string, t: TFunction): string {
  return t(SEVERITY_KEY[normalizeSeverity(severity)]);
}

/**
 * Map a severity to a design-token CSS color used as the row accent.
 * Tokens only — no hardcoded colors. `info` keeps the neutral border tone
 * so non-noteworthy rows stay calm; `warn` / `error` escalate.
 */
export function severityAccentVar(severity: string): string {
  switch (normalizeSeverity(severity)) {
    case 'error':
      return 'var(--nf-color-danger)';
    case 'warn':
      return 'var(--nf-color-warning)';
    default:
      return 'var(--nf-color-border)';
  }
}

/**
 * Map a source to a design-token CSS color used for its chip outline so the
 * three streams stay distinguishable at a glance. Tokens only.
 */
export function sourceAccentVar(source: string): string {
  switch (source) {
    case 'ai':
      return 'var(--nf-color-accent)';
    case 'mcp':
      return 'var(--nf-color-info, var(--nf-color-accent))';
    default:
      return 'var(--nf-color-fg-muted)';
  }
}
