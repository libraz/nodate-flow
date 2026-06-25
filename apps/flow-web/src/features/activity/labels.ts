/**
 * Activity-feed label helpers — turn raw backend enums (`source`,
 * `severity`, `actorKind`) and dotted `action` codes into localized,
 * human-readable strings.
 *
 * Mirrors the timeline `event_kind` idiom: dotted codes are normalized to
 * an underscore slug and looked up under a fixed i18n catalog
 * (`activity:action.<slug>`); anything outside the catalog falls back to a
 * humanized form of the raw code so newly-added backend actions still read
 * sensibly instead of rendering nothing.
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

/**
 * Resolve a localized label for a raw `action` code. Consults the fixed
 * `activity:action.*` catalog and falls back to {@link humanizeAction} for
 * unknown codes so the row never renders an empty or broken label.
 */
export function actionLabel(action: string, t: TFunction): string {
  const slug = actionSlug(action);
  return t(`action.${slug}`, { defaultValue: humanizeAction(action) });
}

const KNOWN_SOURCES: readonly ActivitySource[] = ['audit', 'ai', 'mcp'];

function isKnownSource(value: string): value is ActivitySource {
  return (KNOWN_SOURCES as readonly string[]).includes(value);
}

/** Localized label for an originating stream (`audit` | `ai` | `mcp`). */
export function sourceLabel(source: string, t: TFunction): string {
  if (isKnownSource(source)) return t(`source.${source}`);
  return t(`source.${source}`, { defaultValue: humanizeAction(source) });
}

const KNOWN_KINDS: readonly ActivityActorKind[] = ['user', 'agent', 'system'];

function isKnownKind(value: string): value is ActivityActorKind {
  return (KNOWN_KINDS as readonly string[]).includes(value);
}

/** Localized label for an actor classification. */
export function actorKindLabel(actorKind: string, t: TFunction): string {
  if (isKnownKind(actorKind)) return t(`actor_kind.${actorKind}`);
  return t(`actor_kind.${actorKind}`, { defaultValue: humanizeAction(actorKind) });
}

const KNOWN_SEVERITIES: readonly ActivitySeverity[] = ['info', 'warn', 'error'];

function isKnownSeverity(value: string): value is ActivitySeverity {
  return (KNOWN_SEVERITIES as readonly string[]).includes(value);
}

/** Normalize a raw severity into the closed `ActivitySeverity` set. */
export function normalizeSeverity(severity: string): ActivitySeverity {
  return isKnownSeverity(severity) ? severity : 'info';
}

/** Localized label for a severity (`info` | `warn` | `error`). */
export function severityLabel(severity: string, t: TFunction): string {
  return t(`severity.${normalizeSeverity(severity)}`);
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
