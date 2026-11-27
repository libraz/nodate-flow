/**
 * The real new-comment form: Cmd/Ctrl+Enter posts the trimmed body, and a
 * blank body or a composing keystroke does not.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import CommentForm from '../comment-form';

const mutateAsync = vi.fn();

vi.mock('../api', () => ({
  useAddTaskComment: () => ({ mutateAsync, isPending: false }),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({ toaster: { show: vi.fn() } }));

describe('<CommentForm> shortcut', () => {
  beforeEach(() => {
    mutateAsync.mockReset();
    mutateAsync.mockResolvedValue(undefined);
  });

  it('posts the body on Ctrl+Enter and clears the box', async () => {
    const user = userEvent.setup();
    renderWithProviders(<CommentForm taskId="t1" workspaceId="ws1" />);
    const box = screen.getByRole('textbox', { name: 'tasks.comments.title' });
    await user.type(box, '  hello  ');
    await user.keyboard('{Control>}{Enter}{/Control}');
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith({ taskId: 't1', body: 'hello' }));
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe(''));
  });

  it('does not post a blank body, a plain Enter, or a composing chord', async () => {
    const user = userEvent.setup();
    renderWithProviders(<CommentForm taskId="t1" workspaceId="ws1" />);
    const box = screen.getByRole('textbox', { name: 'tasks.comments.title' });
    await user.type(box, '   ');
    await user.keyboard('{Control>}{Enter}{/Control}');
    await user.type(box, 'hi{Enter}');
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true, isComposing: true });
    expect(mutateAsync).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toContain('\n');
  });

  it('shows the shortcut hint next to the submit button', () => {
    renderWithProviders(<CommentForm taskId="t1" workspaceId="ws1" />);
    expect(screen.getByText('common.submit_shortcut.hint')).toBeTruthy();
    expect(screen.getByText('Enter')).toBeTruthy();
  });
});
