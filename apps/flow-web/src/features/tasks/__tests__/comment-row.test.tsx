/**
 * Agent-written comments must not load remote images: an injected
 * `![](https://host/x.png)` would be a request the reader never chose.
 * Human comments keep rendering images.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import type { ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../../lib/sdk', () => ({
  sdk: { GET: vi.fn(), POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
  authSdk: { GET: vi.fn(), POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

import type { TaskComment } from '../api';
import CommentRow from '../comment-row';

const IMAGE = 'https://images.example/pixel.png';

function comment(overrides: Partial<TaskComment>): TaskComment {
  return {
    id: 'c-1',
    taskId: 'task-1',
    body: '',
    createdAt: 1_700_000_000,
    authorId: 'user-2',
    authorDisplayName: 'Someone',
    ...overrides,
  } as TaskComment;
}

function renderRow(c: TaskComment): HTMLElement {
  const client = new QueryClient();
  const ui: ReactElement = (
    <QueryClientProvider client={client}>
      <CommentRow
        taskId="task-1"
        workspaceId="ws-1"
        comment={c}
        currentUserId="user-1"
        locale="en"
      />
    </QueryClientProvider>
  );
  return render(ui).container;
}

describe('CommentRow images', () => {
  it('turns an agent comment image into a link, inline form', () => {
    const container = renderRow(
      comment({ authorKind: 'agent', authorAgentId: 'a-1', body: `look ![chart](${IMAGE})` }),
    );
    expect(container.querySelector('img')).toBeNull();
    const link = container.querySelector(`a[href="${IMAGE}"]`);
    expect(link?.textContent).toBe('chart');
  });

  it('turns an agent comment image into a link, reference form', () => {
    const container = renderRow(
      comment({
        authorKind: 'agent',
        authorAgentId: 'a-1',
        body: `![chart][r]\n\n[r]: ${IMAGE}`,
      }),
    );
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector(`a[href="${IMAGE}"]`)).not.toBeNull();
  });

  it('still renders images in a human comment', () => {
    const container = renderRow(comment({ authorKind: 'user', body: `![chart](${IMAGE})` }));
    expect(container.querySelector('img')?.getAttribute('src')).toBe(IMAGE);
  });

  it('treats a comment with no author kind as human', () => {
    const container = renderRow(comment({ body: `![chart](${IMAGE})` }));
    expect(container.querySelector('img')).not.toBeNull();
  });
});
