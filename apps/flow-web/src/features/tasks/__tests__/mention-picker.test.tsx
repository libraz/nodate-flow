/**
 * The `@` mention affordance in the shared body editor.
 *
 * Covers the two halves that have to agree for a mention to work: the
 * picker writes the stable notation the backend reads, and everything
 * that is not a mention — a literal `@`, an Escape — leaves the author's
 * text exactly as they typed it.
 */

import Markdown from '@nodate-flow/ui/primitives/markdown';
import { QueryClient } from '@tanstack/react-query';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import type { WorkspaceMember } from '../../workspaces/api';
import { workspacesKeys } from '../../workspaces/api';
import MarkdownEditor from '../markdown-editor';

const WORKSPACE_ID = 'ws-1';
const ANN_ID = '019649b0-0000-7000-8000-000000000000';
const BEE_ID = '019649b0-0000-7000-8000-000000000001';

const MEMBERS: WorkspaceMember[] = [
  {
    id: 'member-ann',
    userId: ANN_ID,
    displayName: 'Ann Rivers',
    email: 'ann@example.com',
    role: 'admin',
    createdAt: 0,
  },
  {
    id: 'member-bee',
    userId: BEE_ID,
    displayName: 'Bee Marsh',
    email: 'bee@example.com',
    role: 'member',
    createdAt: 0,
  },
];

/** A query client already holding the member list, so nothing is fetched. */
function seededClient(): QueryClient {
  const client = new QueryClient({
    defaultOptions: {
      // `staleTime` keeps the seeded list from being refetched against a
      // backend no test has: the picker reads what the page already
      // fetched, which is the whole point of sharing the members key.
      queries: { retry: false, throwOnError: false, staleTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false, throwOnError: false },
    },
  });
  client.setQueryData(workspacesKeys.members(WORKSPACE_ID), MEMBERS);
  return client;
}

/** Controlled host mirroring how the task page drives the editor. */
function Host({
  initial = '',
  onValue,
  onOuterKeyDown,
}: {
  initial?: string;
  onValue?: (next: string) => void;
  onOuterKeyDown?: (event: { key: string }) => void;
}): ReactElement {
  const [value, setValue] = useState(initial);
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: stands in for an ancestor that listens for Escape; the assertion is that it never hears one.
    <div
      onKeyDown={(event) => {
        onOuterKeyDown?.({ key: event.key });
      }}
    >
      <MarkdownEditor
        value={value}
        onChange={(next) => {
          setValue(next);
          onValue?.(next);
        }}
        workspaceId={WORKSPACE_ID}
        aria-label="body"
      />
      <output data-testid="value">{value}</output>
    </div>
  );
}

describe('mention picker', () => {
  it('opens on @ and lists the workspace members', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host initial="Hi " />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@');

    const listbox = await screen.findByRole('listbox');
    expect(listbox).toBeTruthy();
    const options = screen.getAllByRole('option');
    // Each row names the person and the address that tells two same-named
    // people apart, behind their avatar initials.
    expect(options.map((o) => o.textContent)).toEqual([
      'ARAnn Riversann@example.com',
      'BMBee Marshbee@example.com',
    ]);
    // The field becomes a real combobox only while the list is open.
    expect(textarea.getAttribute('role')).toBe('combobox');
    expect(textarea.getAttribute('aria-expanded')).toBe('true');
    expect(textarea.getAttribute('aria-activedescendant')).toBe(options[0]?.id);
  });

  it('filters as the author keeps typing and inserts the stable notation on Enter', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host initial="Ping " />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@bee');

    await waitFor(() => {
      expect(screen.getAllByRole('option')).toHaveLength(1);
    });

    await user.keyboard('{Enter}');

    await waitFor(() => {
      expect(screen.getByTestId('value').textContent).toBe(`Ping @[Bee Marsh](user:${BEE_ID}) `);
    });
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  it('arrow keys move the active option and select the one they land on', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@');
    await screen.findByRole('listbox');

    await user.keyboard('{ArrowDown}');
    const options = screen.getAllByRole('option');
    expect(textarea.getAttribute('aria-activedescendant')).toBe(options[1]?.id);

    await user.keyboard('{Enter}');
    await waitFor(() => {
      expect(screen.getByTestId('value').textContent).toBe(`@[Bee Marsh](user:${BEE_ID}) `);
    });
  });

  it('Escape closes the picker, inserts nothing, and does not reach an ancestor', async () => {
    const user = userEvent.setup();
    const onOuterKeyDown = vi.fn();
    renderWithProviders(<Host initial="Note " onOuterKeyDown={onOuterKeyDown} />, {
      queryClient: seededClient(),
    });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@an');
    await screen.findByRole('listbox');

    await user.keyboard('{Escape}');

    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByTestId('value').textContent).toBe('Note @an');
    // The overlay stack and any editor-cancel handler live above us; the
    // key that dismissed the picker must not have travelled to them.
    expect(onOuterKeyDown.mock.calls.every(([event]) => event.key !== 'Escape')).toBe(true);
    // The field goes back to being an ordinary textarea.
    expect(textarea.getAttribute('role')).toBeNull();
  });

  it('a literal @ followed by a space leaves the text alone', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host initial="rate " />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@ 20 per hour');

    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByTestId('value').textContent).toBe('rate @ 20 per hour');
  });

  it('closes rather than sitting open when no member matches', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, '@zzz');

    await waitFor(() => {
      expect(screen.queryByRole('listbox')).toBeNull();
    });
    expect(screen.getByTestId('value').textContent).toBe('@zzz');
  });

  it('leaves an email address alone', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Host />, { queryClient: seededClient() });

    const textarea = screen.getByRole('textbox', { name: 'body' });
    await user.type(textarea, 'ann@');

    expect(screen.queryByRole('listbox')).toBeNull();
  });
});

describe('a saved body containing a mention', () => {
  it('renders the mention as a chip naming the person, not a link', () => {
    renderWithProviders(<Markdown>{`Ping @[Ann Rivers](user:${ANN_ID}) today`}</Markdown>);

    expect(screen.queryByRole('link')).toBeNull();
    const chip = document.querySelector(`[data-nf-mention="${ANN_ID}"]`);
    expect(chip).toBeTruthy();
    expect(chip?.textContent).toBe('@Ann Rivers');
    // The `@` belongs to the chip; the surrounding prose keeps its own
    // spacing rather than a stray marker before it.
    expect(chip?.previousSibling?.textContent).toBe('Ping ');
  });

  it('still renders an ordinary link as a link', () => {
    renderWithProviders(<Markdown>{'See [the docs](https://example.com)'}</Markdown>);

    const link = screen.getByRole('link', { name: 'the docs' });
    expect(link.getAttribute('href')).toBe('https://example.com');
  });
});
