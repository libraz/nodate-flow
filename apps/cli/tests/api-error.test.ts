import { describe, expect, it } from 'vitest';

import { apiErrorMessage } from '../src/util/api-error.js';

describe('apiErrorMessage', () => {
  it('uses API problem detail before fallback text', () => {
    expect(apiErrorMessage({ detail: 'Workspace is required' }, 'Request failed')).toBe(
      'Workspace is required',
    );
  });

  it('includes API problem code and user action when present', () => {
    expect(
      apiErrorMessage(
        {
          type: 'WS.TASK.NOT_FOUND',
          detail: 'Task not found',
          userAction: 'Refresh the task list and try again.',
        },
        'Request failed',
      ),
    ).toBe('[WS.TASK.NOT_FOUND] Task not found\nRefresh the task list and try again.');
  });

  it('uses API problem title when detail is absent', () => {
    expect(apiErrorMessage({ title: 'Forbidden' }, 'Request failed')).toBe('Forbidden');
  });

  it('falls back for non-problem errors', () => {
    expect(apiErrorMessage('boom', 'Request failed')).toBe('Request failed');
  });
});
