/**
 * The shared form-level submit chord: Cmd/Ctrl+Enter goes through the
 * form's enabled submit button; everything else is left alone.
 */

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { onSubmitShortcut } from '../submit-shortcut';

function Form({
  onSubmit,
  disabled = false,
  withButton = true,
  onTextareaKeyDown,
}: {
  onSubmit: () => void;
  disabled?: boolean;
  withButton?: boolean;
  onTextareaKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
}): ReactElement {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
      onKeyDown={onSubmitShortcut}
    >
      <textarea aria-label="body" onKeyDown={onTextareaKeyDown} />
      <button type="button">cancel</button>
      {withButton ? (
        <button type="submit" disabled={disabled}>
          send
        </button>
      ) : null}
    </form>
  );
}

describe('onSubmitShortcut', () => {
  it('submits through the enabled submit button on Ctrl+Enter and Cmd+Enter', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} />);
    await user.click(screen.getByRole('textbox', { name: 'body' }));
    await user.keyboard('{Control>}{Enter}{/Control}');
    await user.keyboard('{Meta>}{Enter}{/Meta}');
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it('does nothing when the submit button is disabled', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} disabled />);
    await user.click(screen.getByRole('textbox', { name: 'body' }));
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('does nothing when the form has no submit button', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} withButton={false} />);
    await user.click(screen.getByRole('textbox', { name: 'body' }));
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('keeps plain Enter and Shift+Enter as newlines', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} />);
    const box = screen.getByRole('textbox', { name: 'body' });
    await user.type(box, 'a{Enter}b{Shift>}{Enter}{/Shift}c');
    expect(onSubmit).not.toHaveBeenCalled();
    expect((box as HTMLTextAreaElement).value).toBe('a\nb\nc');
  });

  it('ignores the chord while an input method is composing, including Safari keyCode 229', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} />);
    const box = screen.getByRole('textbox', { name: 'body' });
    await user.click(box);
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true, isComposing: true });
    fireEvent.keyDown(box, { key: 'Enter', ctrlKey: true, keyCode: 229 });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('leaves a key an inner handler already took', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Form onSubmit={onSubmit} onTextareaKeyDown={(e) => e.preventDefault()} />);
    await user.click(screen.getByRole('textbox', { name: 'body' }));
    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
