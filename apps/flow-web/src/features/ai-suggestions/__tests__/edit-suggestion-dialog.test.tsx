import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';
import EditSuggestionDialog from '../edit-suggestion-dialog';
import type { AiSuggestion } from '../store';

const baseSuggestion: AiSuggestion = {
  eventId: 'proposal-1',
  proposalId: 'proposal-1',
  inboxItemId: 'inbox-1',
  revision: 0,
  sourceVersionStatus: 'captured',
  proposedAt: 1_700_000_000,
  recommendedAction: 'open',
  reasoning: 'Initial reasoning',
  score: 0.9,
};

describe('EditSuggestionDialog', () => {
  it('clears the inline error when reopening with the same suggestion', async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    const onClose = vi.fn();

    const { rerender } = renderWithProviders(
      <EditSuggestionDialog suggestion={baseSuggestion} open onClose={onClose} onSave={onSave} />,
    );

    // Trigger validation: clear the reasoning, then submit.
    const textarea = screen.getByRole('textbox');
    await user.clear(textarea);
    await user.click(screen.getByRole('button', { name: 'edit.save' }));

    // The error message becomes visible.
    expect(screen.getByText('edit.reasoning_required')).toBeTruthy();

    // Close the dialog and reopen — the error should not survive.
    rerender(
      <EditSuggestionDialog
        suggestion={baseSuggestion}
        open={false}
        onClose={onClose}
        onSave={onSave}
      />,
    );
    rerender(
      <EditSuggestionDialog suggestion={baseSuggestion} open onClose={onClose} onSave={onSave} />,
    );

    expect(screen.queryByText('edit.reasoning_required')).toBeNull();
  });
});
