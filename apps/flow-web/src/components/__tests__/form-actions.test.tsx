/**
 * FormActions puts the shortcut hint at the start of the row and leaves it
 * out unless asked.
 */

import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';

import FormActions from '../form-actions';

describe('<FormActions>', () => {
  it('renders the hint before the buttons', () => {
    renderWithProviders(
      <FormActions hint>
        <button type="submit">go</button>
      </FormActions>,
    );
    const hint = screen.getByText('common.submit_shortcut.hint');
    const button = screen.getByRole('button', { name: 'go' });
    expect(hint.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(hint.hasAttribute('data-disabled')).toBe(false);
  });

  it('omits the hint by default', () => {
    renderWithProviders(
      <FormActions>
        <button type="submit">go</button>
      </FormActions>,
    );
    expect(screen.queryByText('common.submit_shortcut.hint')).toBeNull();
  });

  it('dims the hint when the submit button is disabled', () => {
    renderWithProviders(
      <FormActions hint={{ disabled: true }}>
        <button type="submit" disabled>
          go
        </button>
      </FormActions>,
    );
    expect(screen.getByText('common.submit_shortcut.hint').hasAttribute('data-disabled')).toBe(
      true,
    );
  });

  it('shows ↵ keycaps', () => {
    renderWithProviders(
      <FormActions hint>
        <button type="submit">go</button>
      </FormActions>,
    );
    expect(screen.getByText('↵').tagName).toBe('KBD');
  });
});
