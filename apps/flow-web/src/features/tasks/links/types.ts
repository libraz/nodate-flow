/**
 * Local types for the linked-events feature.
 *
 * Re-exports the SDK-generated `TaskEventLink` and narrows the relation
 * field to the two kinds the manual-link UI surfaces. The backend still
 * accepts `depends_on` / `prep_for` for AI-driven flows but the manual
 * picker only ever creates `contributes_to` or `blocks` links.
 */

import type { components } from '@nodate-flow/sdk';

/** Raw SDK shape returned from `/tasks/{id}/linked-events`. */
export type TaskEventLink = components['schemas']['TaskEventLink'];

/** Relations the manual picker can produce. */
export type LinkKind = 'contributes_to' | 'blocks';

/** All relation kinds the backend recognises (manual + AI-driven). */
export type LinkRelation = 'contributes_to' | 'blocks' | 'depends_on' | 'prep_for';

/** Calendar event row returned by the workspace `/calendar-events` listing. */
export type CalendarEventListItem = components['schemas']['CrossCalendarEventResponse'];
