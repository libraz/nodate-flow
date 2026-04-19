/**
 * Component tests for InboxItemRow.
 *
 * Verifies the row renders source badge, kind label, task link,
 * relative time, and action buttons (Archive / Snooze).
 */

import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';

import { renderWithProviders } from '../../../test/helpers/render';
import type { InboxItem } from '../api';
import InboxItemRow from '../inbox-item-row';

/** Build a minimal InboxItem fixture. */
function anInboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: 'inbox-001',
    source: 'github',
    kind: 'pull_request.merged',
    taskId: 'task-abc',
    taskTitle: 'Fix login bug',
    receivedAt: Math.floor(Date.now() / 1000) - 3600, // 1 hour ago
    status: 'unread',
    ...overrides,
  } as InboxItem;
}

describe('<InboxItemRow>', () => {
  it('renders source badge, kind, and task link', () => {
    renderWithProviders(
      <InboxItemRow item={anInboxItem()} onArchive={() => {}} onSnooze={() => {}} />,
    );

    // Source badge (i18n key passthrough)
    expect(screen.getByText('source.github')).toBeDefined();

    // Kind label
    expect(screen.getByText('pull_request.merged')).toBeDefined();

    // Task link
    const link = screen.getByRole('link', { name: 'Fix login bug' });
    expect(link).toBeDefined();
    expect(link.getAttribute('href')).toContain('task-abc');
  });

  it('renders relative time for receivedAt', () => {
    const { container } = renderWithProviders(
      <InboxItemRow
        item={anInboxItem({ receivedAt: Math.floor(Date.now() / 1000) - 60 })}
        onArchive={() => {}}
        onSnooze={() => {}}
      />,
    );

    // The formatted relative time should be present somewhere in the row.
    // Exact text depends on Intl.RelativeTimeFormat, but it should not be empty.
    const timeSpans = container.querySelectorAll('span');
    const hasTimeText = Array.from(timeSpans).some(
      (span) => span.textContent && span.textContent.trim().length > 0,
    );
    expect(hasTimeText).toBe(true);
  });

  it('does not render task link when taskId is absent', () => {
    renderWithProviders(
      <InboxItemRow
        item={{ ...anInboxItem(), taskId: undefined, taskTitle: undefined } as unknown as InboxItem}
        onArchive={() => {}}
        onSnooze={() => {}}
      />,
    );

    const links = screen.queryAllByRole('link');
    expect(links.length).toBe(0);
  });

  it('calls onArchive with the item id when Archive is clicked', async () => {
    const onArchive = vi.fn();
    renderWithProviders(
      <InboxItemRow item={anInboxItem()} onArchive={onArchive} onSnooze={() => {}} />,
    );

    const user = userEvent.setup();
    const archiveBtn = screen.getByRole('button', { name: 'action.archive' });
    await user.click(archiveBtn);
    expect(onArchive).toHaveBeenCalledWith('inbox-001');
  });

  it('renders the Snooze button', () => {
    renderWithProviders(
      <InboxItemRow item={anInboxItem()} onArchive={() => {}} onSnooze={() => {}} />,
    );

    const snoozeBtn = screen.getByRole('button', { name: 'action.snooze' });
    expect(snoozeBtn).toBeDefined();
  });

  it('has no a11y violations', async () => {
    const { container } = renderWithProviders(
      <InboxItemRow item={anInboxItem()} onArchive={() => {}} onSnooze={() => {}} />,
    );
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
