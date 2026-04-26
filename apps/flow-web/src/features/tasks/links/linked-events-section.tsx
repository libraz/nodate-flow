/**
 * LinkedEventsSection — orchestrator for the task↔calendar-event link
 * sub-section on the task detail page.
 *
 * Composition:
 * - header: a disclosure caret + title + count + a ghost "Link event"
 *   button that opens the popover picker.
 * - body (when expanded): empty state, list of `LinkedEventRow`s, and
 *   the picker popover anchored to the trigger button.
 *
 * Disclosure state is persisted in localStorage via
 * `useCollapsibleState`. The default is open when there are no links
 * (so the empty-state CTA is visible) or up to five links; it
 * collapses by default when the list is long enough that the user
 * probably wants to see other sections first.
 *
 * Mutations are routed through `useLinkEvent` / `useUnlinkEvent` which
 * apply optimistic updates to the linked-events query cache. The
 * section surfaces success / failure via the global `toaster`.
 *
 * The component scopes a local ErrorBoundary around the suspense query
 * so a failed initial GET renders `LinkedEventsError` in place of the
 * section body — escalations to the route-level FatalFallback are
 * reserved for genuinely fatal failures elsewhere on the page.
 */

import Button from '@nodate-flow/ui/primitives/button';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useQueryClient } from '@tanstack/react-query';
import { type ReactElement, useRef, useState } from 'react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { ApiError } from '../../../lib/api-error';
import EventPicker from './event-picker';
import { useCollapsibleState } from './hooks/use-collapsible-state';
import { useLinkEvent } from './hooks/use-link-event';
import { linkedEventsKeys, useLinkedEventsQuery } from './hooks/use-linked-events';
import { useUnlinkEvent } from './hooks/use-unlink-event';
import LinkedEventRow from './linked-event-row';
import LinkedEventsEmpty from './linked-events-empty';
import LinkedEventsError from './linked-events-error';
import styles from './linked-events.module.css';
import type { CalendarEventListItem, LinkKind, TaskEventLink } from './types';

const COLLAPSE_KEY = 'linked-events';
const COLLAPSE_THRESHOLD = 5;
const ALREADY_LINKED_CODE_FRAGMENTS: readonly string[] = ['ALREADY_LINKED', 'EVENT_ALREADY_LINKED'];

export interface LinkedEventsSectionProps {
  taskId: string;
  workspaceId: string;
  locale: string;
}

/**
 * Heuristic: an apierr code that looks like the backend's
 * "this event is already linked" invariant. We match permissively
 * because the exact code is owned by `errors/*.yaml` and can drift
 * without breaking the toast UX.
 */
function isAlreadyLinkedError(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  const code = error.code ?? '';
  return ALREADY_LINKED_CODE_FRAGMENTS.some((fragment) => code.includes(fragment));
}

/**
 * Public wrapper that scopes a local ErrorBoundary around the suspense
 * query so a failed initial GET renders the section's own
 * `LinkedEventsError` fallback instead of escalating to the route-level
 * FatalFallback. Resetting the boundary invalidates the query so the
 * Retry button refetches transparently.
 */
export default function LinkedEventsSection(props: LinkedEventsSectionProps): ReactElement {
  const queryClient = useQueryClient();
  return (
    <ErrorBoundary
      FallbackComponent={LinkedEventsError}
      onReset={() => {
        queryClient.invalidateQueries({ queryKey: linkedEventsKeys.list(props.taskId) });
      }}
    >
      <LinkedEventsSectionImpl {...props} />
    </ErrorBoundary>
  );
}

function LinkedEventsSectionImpl({
  taskId,
  workspaceId,
  locale,
}: LinkedEventsSectionProps): ReactElement {
  const { t } = useTranslation('linkedEvents');
  const { data } = useLinkedEventsQuery(taskId);
  const link = useLinkEvent();
  const unlink = useUnlinkEvent();
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);

  const links = data.links;
  const total = data.total;
  const initialCollapsed = links.length > COLLAPSE_THRESHOLD;
  const { collapsed, toggle } = useCollapsibleState(COLLAPSE_KEY, initialCollapsed);

  const linkedEventIds: ReadonlySet<string> = new Set(
    links.map((entry) => entry.eventId).filter((id): id is string => typeof id === 'string'),
  );

  const handleLink = (args: { event: CalendarEventListItem; kind: LinkKind }): void => {
    const eventId = args.event.id;
    if (!eventId) return;
    const title = args.event.title ?? '';
    const preview: NonNullable<Parameters<typeof link.mutate>[0]['preview']> = {
      title,
      ...(args.event.calendarId !== undefined ? { calendarId: args.event.calendarId } : {}),
      ...(args.event.startAt !== undefined ? { eventStartAt: args.event.startAt } : {}),
      ...(args.event.endAt !== undefined ? { eventEndAt: args.event.endAt } : {}),
      ...(args.event.allDay !== undefined ? { eventAllDay: args.event.allDay } : {}),
    };
    link.mutate(
      { taskId, eventId, kind: args.kind, preview },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('toast.linked', { title }) });
        },
        onError: (error) => {
          if (isAlreadyLinkedError(error)) {
            toaster.show({ tone: 'warning', message: t('toast.alreadyLinkedError') });
            return;
          }
          toaster.show({ tone: 'danger', message: t('toast.linkFailed') });
        },
      },
    );
    setPickerOpen(false);
  };

  const handleUnlink = (linkId: string): void => {
    const removed: TaskEventLink | undefined = links.find((entry) => entry.id === linkId);
    const title = removed?.eventTitle ?? '';
    unlink.mutate(
      { taskId, linkId },
      {
        onSuccess: () => {
          toaster.show({ tone: 'success', message: t('toast.unlinked', { title }) });
        },
        onError: () => {
          toaster.show({ tone: 'danger', message: t('toast.unlinkFailed') });
        },
      },
    );
  };

  const titleId = `${COLLAPSE_KEY}-title`;
  const bodyId = `${COLLAPSE_KEY}-body`;
  const isEmpty = links.length === 0;

  return (
    <section className={styles.section} aria-labelledby={titleId}>
      <div className={styles.sectionHeader}>
        <button
          type="button"
          className={styles.disclosure}
          aria-expanded={!collapsed}
          aria-controls={bodyId}
          onClick={toggle}
        >
          <svg
            className={styles.caret}
            data-open={collapsed ? undefined : 'true'}
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M6 4l4 4-4 4" />
          </svg>
          <h3 id={titleId} className={styles.title}>
            {t('section.title')}
          </h3>
          <span className={styles.count}>{t('section.count', { count: total })}</span>
        </button>
        <div className={styles.headerActions} style={{ position: 'relative' }}>
          <Button
            ref={triggerRef}
            type="button"
            variant="ghost"
            size="sm"
            aria-haspopup="dialog"
            aria-expanded={pickerOpen}
            onClick={() => {
              setPickerOpen((prev) => !prev);
            }}
          >
            {t('trigger.linkEvent')}
          </Button>
          <EventPicker
            workspaceId={workspaceId}
            taskId={taskId}
            alreadyLinkedEventIds={linkedEventIds}
            locale={locale}
            anchorRef={triggerRef}
            isOpen={pickerOpen}
            onClose={() => {
              setPickerOpen(false);
            }}
            onLink={handleLink}
          />
        </div>
      </div>

      <div id={bodyId} className={styles.body} hidden={collapsed}>
        {isEmpty ? (
          <LinkedEventsEmpty
            onTriggerClick={() => {
              setPickerOpen(true);
            }}
          />
        ) : (
          <ul className={styles.list}>
            {links.map((entry) => (
              <LinkedEventRow
                key={entry.id}
                link={entry}
                locale={locale}
                onUnlink={handleUnlink}
                isOptimistic={entry.id.startsWith('optimistic-')}
              />
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
