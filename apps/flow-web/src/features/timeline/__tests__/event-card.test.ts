/**
 * The rules that decide which lane color and label a timeline row wears.
 */

import { describe, expect, it } from 'vitest';
import { INTEGRATION_SOURCE_COLORS as SOURCE_COLOR } from '../../../lib/source-colors';
import { eventSourceTag } from '../event-card';

const SYSTEM_COLOR = 'var(--nf-color-fg-muted)';

describe('eventSourceTag', () => {
  describe('signal events', () => {
    it('reads the external origin out of the payload, not the type', () => {
      expect(eventSourceTag('signal.attached', { source: 'github' })).toEqual({
        label: 'github',
        color: SOURCE_COLOR.github,
      });
    });

    it('tags a Slack-sourced signal with the Slack brand color', () => {
      expect(eventSourceTag('signal.attached', { source: 'slack' })).toEqual({
        label: 'slack',
        color: SOURCE_COLOR.slack,
      });
    });

    it('tags a Google-sourced signal with the Google brand color', () => {
      expect(eventSourceTag('signal.attached', { source: 'google' })).toEqual({
        label: 'google',
        color: SOURCE_COLOR.google,
      });
    });

    it('shows a generic webhook under the Google brand it arrives through', () => {
      expect(eventSourceTag('signal.attached', { source: 'webhook' })).toEqual({
        label: 'google',
        color: SOURCE_COLOR.google,
      });
    });

    it('falls back to the plain signal lane for an unrecognised source', () => {
      expect(eventSourceTag('signal.attached', { source: 'pagerduty' })).toEqual({
        label: 'signal',
        color: SOURCE_COLOR.signal,
      });
    });

    it('falls back to the plain signal lane when the payload carries no source', () => {
      expect(eventSourceTag('signal.judged', {})).toEqual({
        label: 'signal',
        color: SOURCE_COLOR.signal,
      });
    });

    it('falls back to the plain signal lane when there is no payload at all', () => {
      expect(eventSourceTag('signal.attached')).toEqual({
        label: 'signal',
        color: SOURCE_COLOR.signal,
      });
    });
  });

  describe('machine lane', () => {
    it('tags an AI suggestion as AI', () => {
      expect(eventSourceTag('ai.suggestion.proposed')).toEqual({
        label: 'ai',
        color: SOURCE_COLOR.ai,
      });
    });

    it('tags an MCP tool invocation as AI', () => {
      expect(eventSourceTag('mcp.tool.invoked')).toEqual({
        label: 'ai',
        color: SOURCE_COLOR.ai,
      });
    });

    it('tags an agent working a task as AI rather than as system noise', () => {
      expect(eventSourceTag('agent.task.attached')).toEqual({
        label: 'ai',
        color: SOURCE_COLOR.ai,
      });
    });

    it('tags an agent run outcome as AI', () => {
      expect(eventSourceTag('ai.agent.run.failed')).toEqual({
        label: 'ai',
        color: SOURCE_COLOR.ai,
      });
    });

    it('moves a model-written page into the AI lane on the payload flag', () => {
      expect(eventSourceTag('page.created', { pageId: 'p1', isAiGenerated: true })).toEqual({
        label: 'ai',
        color: SOURCE_COLOR.ai,
      });
    });

    it('leaves a hand-written page in the human lane when the flag is absent', () => {
      expect(eventSourceTag('page.created', { pageId: 'p1' })).toEqual({
        label: 'page',
        color: SOURCE_COLOR.task,
      });
    });

    it('leaves a page in the human lane when the flag is present but false', () => {
      expect(eventSourceTag('page.created', { isAiGenerated: false })).toEqual({
        label: 'page',
        color: SOURCE_COLOR.task,
      });
    });
  });

  describe('human lane', () => {
    it('tags a task event as a task', () => {
      expect(eventSourceTag('task.updated', { field: 'status' })).toEqual({
        label: 'task',
        color: SOURCE_COLOR.task,
      });
    });

    it('gives a mention the same tag as the edit it was written in', () => {
      expect(eventSourceTag('mention.created')).toEqual({
        label: 'task',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags a standalone comment event as a comment', () => {
      expect(eventSourceTag('comment.added')).toEqual({
        label: 'comment',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags a page update as a page', () => {
      expect(eventSourceTag('page.updated')).toEqual({
        label: 'page',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags a project event as a project', () => {
      expect(eventSourceTag('project.created')).toEqual({
        label: 'project',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags a workspace membership change as a workspace event', () => {
      expect(eventSourceTag('workspace.member.added')).toEqual({
        label: 'workspace',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags a calendar event a person edited as a calendar event', () => {
      expect(eventSourceTag('calendar.event.updated', { calendarEventId: 'e1' })).toEqual({
        label: 'calendar',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags the whole calendar family, not just the event lifecycle', () => {
      const tags = [
        'calendar.created',
        'calendar.member.added',
        'calendar.event.created',
        'calendar.event.comment.created',
        'calendar.event.attendee.added',
        'calendar.memo.created',
      ].map((type) => eventSourceTag(type));
      expect(tags).toEqual(tags.map(() => ({ label: 'calendar', color: SOURCE_COLOR.task })));
    });

    it('gives a public share its own label, so what was published stands out', () => {
      expect(eventSourceTag('public_share.created')).toEqual({
        label: 'share',
        color: SOURCE_COLOR.task,
      });
    });

    it('tags every public-share change, not only the share itself', () => {
      const tags = [
        'public_share.events_attached',
        'public_share.event_detached',
        'public_share.rotated',
      ].map((type) => eventSourceTag(type));
      expect(tags).toEqual(tags.map(() => ({ label: 'share', color: SOURCE_COLOR.task })));
    });

    it('keeps every human-lane prefix on one color so only the label differs', () => {
      const colors = [
        'task.created',
        'mention.created',
        'comment.added',
        'page.updated',
        'project.created',
        'workspace.member.added',
        'calendar.event.created',
        'public_share.created',
      ].map((type) => eventSourceTag(type).color);
      expect(new Set(colors)).toEqual(new Set([SOURCE_COLOR.task]));
    });
  });

  describe('scheduler-emitted events', () => {
    it('leaves a calendar reminder in the system lane, with no person or model behind it', () => {
      expect(eventSourceTag('calendar.reminder', { calendarEventId: 'e1' })).toEqual({
        label: 'system',
        color: SYSTEM_COLOR,
      });
    });

    it('does not sweep the reminder into the human lane along with its namesakes', () => {
      expect(eventSourceTag('calendar.reminder').label).not.toBe(
        eventSourceTag('calendar.event.created').label,
      );
    });
  });

  describe('unrecognised events', () => {
    it('falls through to the system tag for an unknown prefix', () => {
      expect(eventSourceTag('timebox.activated')).toEqual({
        label: 'system',
        color: SYSTEM_COLOR,
      });
    });

    it('falls through to the system tag for a type with no prefix at all', () => {
      expect(eventSourceTag('heartbeat')).toEqual({
        label: 'system',
        color: SYSTEM_COLOR,
      });
    });

    it('does not treat a prefix that merely starts with a known word as that lane', () => {
      expect(eventSourceTag('pages.reordered')).toEqual({
        label: 'system',
        color: SYSTEM_COLOR,
      });
    });
  });
});
