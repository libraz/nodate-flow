/**
 * The kinds the filter chips claim to cover, and the labels they wear in
 * every locale.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { KIND_GROUPS } from '../event-filter-bar';

const LOCALES = ['en', 'ja', 'zh'] as const;

interface TimelineLocale {
  filter: { kind_group: Record<string, string> };
  event_kind: Record<string, string>;
}

function loadLocale(locale: string): TimelineLocale {
  const path = resolve(__dirname, `../../../../locales/${locale}/timeline.json`);
  return JSON.parse(readFileSync(path, 'utf-8')) as TimelineLocale;
}

const allChips = KIND_GROUPS.flatMap((g) => g.chips);
const allKinds = allChips.flatMap((c) => [...c.kinds]);

describe('KIND_GROUPS', () => {
  it('offers a chip for a mention, so a mention row can be isolated', () => {
    expect(allKinds).toContain('mention.created');
  });

  it('covers the page family the feed carries', () => {
    expect(allKinds).toEqual(
      expect.arrayContaining([
        'page.created',
        'page.updated',
        'page.disabled',
        'page.archived',
        'page.unarchived',
      ]),
    );
  });

  it('covers workspace membership separately from task membership', () => {
    const workspace = KIND_GROUPS.find((g) => g.key === 'workspace');
    const member = KIND_GROUPS.find((g) => g.key === 'member');
    expect(workspace?.chips.flatMap((c) => [...c.kinds])).toEqual([
      'workspace.member.added',
      'workspace.member.removed',
      'workspace.member.role_changed',
    ]);
    expect(member?.chips.flatMap((c) => [...c.kinds])).toEqual([
      'task.actor.added',
      'task.actor.removed',
    ]);
  });

  it('selects the historical comment kind through the current comment chip', () => {
    const added = allChips.find((c) => c.key === 'task.comment.added');
    expect(added?.kinds).toEqual(['task.comment.added', 'comment.added']);
  });

  it('gives the legacy comment kind no chip of its own', () => {
    expect(allChips.map((c) => c.key)).not.toContain('comment.added');
  });

  it('splits the AI vocabulary by the question a reader is asking', () => {
    const byKey = Object.fromEntries(
      KIND_GROUPS.map((g) => [g.key, g.chips.flatMap((c) => [...c.kinds])]),
    );
    expect(byKey.ai).toEqual([
      'ai.suggestion.proposed',
      'ai.suggestion.applied',
      'ai.suggestion.dismissed',
      'ai.suggestion.edited',
      'ai.auto_action.proposed',
    ]);
    expect(byKey.agent).toEqual([
      'agent.task.attached',
      'agent.task.detached',
      'agent.task.thought',
      'agent.task.handoff_to_user',
      'agent.task.handoff_to_agent',
    ]);
    expect(byKey.agent_runtime).toEqual([
      'ai.agent.paused',
      'ai.agent.resumed',
      'ai.agent.run.started',
      'ai.agent.run.completed',
      'ai.agent.run.failed',
    ]);
  });

  it('offers no chip for a prefix the backend has no producer for', () => {
    const unproducible = allKinds.filter(
      (k) => k.startsWith('project.') || k.startsWith('mcp.') || k === 'workspace.updated',
    );
    expect(unproducible).toEqual([]);
  });

  it('never lists the same kind under two chips', () => {
    expect(new Set(allKinds).size).toBe(allKinds.length);
  });
});

describe.each(LOCALES)('timeline locale %s', (locale) => {
  const messages = loadLocale(locale);

  it('names every filter group', () => {
    const missing = KIND_GROUPS.map((g) => g.key).filter(
      (key) => messages.filter.kind_group[key] === undefined,
    );
    expect(missing).toEqual([]);
  });

  it('labels every chip, so none falls back to a raw dotted identifier', () => {
    const missing = allChips
      .map((c) => c.key.replace(/\./g, '_'))
      .filter((key) => messages.event_kind[key] === undefined);
    expect(missing).toEqual([]);
  });

  it('gives the chips in a group labels a reader can tell apart', () => {
    const collisions = KIND_GROUPS.flatMap((g) => {
      const labels = g.chips.map((c) => messages.event_kind[c.key.replace(/\./g, '_')]);
      const repeated = labels.filter((l, i) => labels.indexOf(l) !== i);
      return [...new Set(repeated)].map((l) => `${g.key}: ${String(l)}`);
    });
    expect(collisions).toEqual([]);
  });
});
