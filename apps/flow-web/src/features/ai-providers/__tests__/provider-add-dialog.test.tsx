/**
 * A base URL on kind=openai is a configuration the server refuses to
 * build. It used to be offered in this form, stored without complaint,
 * and then break every AI call in the workspace — the default provider is
 * the most recently created enabled row, so one bad row shadowed a
 * working one and the symptom was "the AI stopped", pointing nowhere near
 * the row that had just been added.
 *
 * The field is now offered only for the kinds that take one, and a value
 * typed under one of those does not survive a switch to openai: hidden
 * state that still gets submitted is the same bug wearing a different
 * coat.
 */

import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';

import ProviderAddDialog from '../provider-add-dialog';

function renderDialog() {
  return renderWithProviders(
    <ProviderAddDialog workspaceId="ws-1" open onClose={() => undefined} />,
  );
}

/** The kind select, addressed by its already-rendered label. */
function kindSelect(): HTMLSelectElement {
  return screen.getByLabelText(/providers\.field\.kind/) as HTMLSelectElement;
}

function baseUrlInput(): HTMLElement | null {
  return screen.queryByLabelText(/providers\.field\.base_url/);
}

describe('ProviderAddDialog', () => {
  it('offers a base URL for the kinds that take one', async () => {
    const user = userEvent.setup();
    renderDialog();

    // anthropic is the initial kind and accepts a base URL.
    expect(baseUrlInput()).not.toBeNull();

    await user.selectOptions(kindSelect(), 'openai_compat');
    expect(baseUrlInput()).not.toBeNull();

    await user.selectOptions(kindSelect(), 'ollama');
    expect(baseUrlInput()).not.toBeNull();
  });

  it('does not offer a base URL for kind=openai', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.selectOptions(kindSelect(), 'openai');
    expect(baseUrlInput()).toBeNull();
  });

  it('drops a base URL typed under another kind when switching to openai', async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.selectOptions(kindSelect(), 'openai_compat');
    const field = baseUrlInput();
    if (!field) throw new Error('base URL field missing for openai_compat');
    await user.type(field, 'https://proxy.internal/v1');
    expect((field as HTMLInputElement).value).toBe('https://proxy.internal/v1');

    await user.selectOptions(kindSelect(), 'openai');
    expect(baseUrlInput()).toBeNull();

    // Back to a kind that shows the field: it must be empty, not carrying
    // the value that was hidden a moment ago.
    await user.selectOptions(kindSelect(), 'openai_compat');
    const again = baseUrlInput();
    if (!again) throw new Error('base URL field missing after switching back');
    expect((again as HTMLInputElement).value).toBe('');
  });
});
