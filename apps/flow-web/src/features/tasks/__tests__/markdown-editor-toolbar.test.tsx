/**
 * Smoke test for the MarkdownEditor toolbar glyphs.
 *
 * Guards against regression of CLAUDE.md rule #21 (no HTML character
 * references / unicode escapes — UTF-8 only). The list and link
 * buttons must render the literal "•" and "🔗" characters, not
 * &#8226;/&#128279; or &bull;/escape sequences.
 */

import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../../test/helpers/render';
import MarkdownEditor from '../markdown-editor';

describe('<MarkdownEditor> toolbar glyphs', () => {
  it('renders the list and link buttons with raw UTF-8 glyphs', () => {
    renderWithProviders(
      <MarkdownEditor
        value=""
        onChange={() => {
          // no-op; we only inspect the toolbar
        }}
      />,
    );

    const listButton = screen.getByRole('button', { name: /tasks\.markdown_editor\.list/i });
    expect(listButton.textContent).toBe('•');

    const linkButton = screen.getByRole('button', { name: /tasks\.markdown_editor\.link/i });
    expect(linkButton.textContent).toBe('\u{1F517}');
  });
});
