/**
 * Unit tests for the activity-feed label helpers.
 *
 * The action/source/severity lookups must:
 *  - return a catalog translation when the key exists,
 *  - fall back to a humanized form of the raw code for unknown actions
 *    (never render nothing),
 *  - normalize severities to the closed set and map them to design tokens.
 */

import type { TFunction } from 'i18next';
import { describe, expect, it } from 'vitest';

import {
  actionLabel,
  actionSlug,
  actorKindLabel,
  humanizeAction,
  normalizeSeverity,
  severityAccentVar,
  severityLabel,
  sourceAccentVar,
  sourceLabel,
} from '../labels';

/**
 * Build a fake i18next `TFunction` backed by a fixed catalog. Mirrors
 * i18next semantics: a known key returns its value; an unknown key returns
 * the supplied `defaultValue` (or the key itself when none is given).
 */
function makeT(catalog: Record<string, string>): TFunction {
  const t = (key: string, opts?: { defaultValue?: string }): string => {
    if (key in catalog) return catalog[key] as string;
    return opts?.defaultValue ?? key;
  };
  return t as unknown as TFunction;
}

describe('actionSlug', () => {
  it('replaces dots with underscores', () => {
    expect(actionSlug('task.create')).toBe('task_create');
    expect(actionSlug('calendar.event.update')).toBe('calendar_event_update');
  });
});

describe('humanizeAction', () => {
  it('title-cases dotted and underscored codes', () => {
    expect(humanizeAction('task.create')).toBe('Task create');
    expect(humanizeAction('calendar.event.update')).toBe('Calendar event update');
    expect(humanizeAction('mcp_token_delete')).toBe('Mcp token delete');
  });

  it('ignores empty segments', () => {
    expect(humanizeAction('task..create')).toBe('Task create');
  });
});

describe('actionLabel', () => {
  const t = makeT({ 'action.task_create': 'created a task' });

  it('returns the catalog translation for a known action', () => {
    expect(actionLabel('task.create', t)).toBe('created a task');
  });

  it('falls back to a humanized form for an unknown action', () => {
    expect(actionLabel('calendar.event.update', t)).toBe('Calendar event update');
  });
});

describe('sourceLabel / actorKindLabel', () => {
  const t = makeT({
    'source.audit': 'Audit',
    'actor_kind.agent': 'Agent',
  });

  it('translates known sources and kinds', () => {
    expect(sourceLabel('audit', t)).toBe('Audit');
    expect(actorKindLabel('agent', t)).toBe('Agent');
  });

  it('humanizes unknown sources / kinds rather than rendering a raw key', () => {
    expect(sourceLabel('webhook', t)).toBe('Webhook');
    expect(actorKindLabel('robot', t)).toBe('Robot');
  });
});

describe('normalizeSeverity', () => {
  it('passes through known severities', () => {
    expect(normalizeSeverity('info')).toBe('info');
    expect(normalizeSeverity('warn')).toBe('warn');
    expect(normalizeSeverity('error')).toBe('error');
  });

  it('defaults unknown severities to info', () => {
    expect(normalizeSeverity('critical')).toBe('info');
    expect(normalizeSeverity('')).toBe('info');
  });
});

describe('severityLabel', () => {
  const t = makeT({
    'severity.info': 'Info',
    'severity.warn': 'Warning',
    'severity.error': 'Error',
  });

  it('labels via the normalized severity', () => {
    expect(severityLabel('error', t)).toBe('Error');
    expect(severityLabel('bogus', t)).toBe('Info');
  });
});

describe('severityAccentVar', () => {
  it('maps severities to design tokens only', () => {
    expect(severityAccentVar('error')).toBe('var(--nf-color-danger)');
    expect(severityAccentVar('warn')).toBe('var(--nf-color-warning)');
    expect(severityAccentVar('info')).toBe('var(--nf-color-border)');
    expect(severityAccentVar('unknown')).toBe('var(--nf-color-border)');
  });
});

describe('sourceAccentVar', () => {
  it('maps every source to a var(--nf-*) token', () => {
    for (const v of ['audit', 'ai', 'mcp', 'other']) {
      expect(sourceAccentVar(v)).toMatch(/^var\(--nf-/);
    }
  });
});
