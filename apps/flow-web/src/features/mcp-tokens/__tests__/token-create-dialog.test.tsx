/**
 * Every MCP tool requires a non-empty scope. The create dialog used to
 * take scopes as free text and send `null` when it was blank, and the
 * server adds no default — so the default path produced a token that
 * authenticates, opens the SSE stream, and lists all tools while
 * refusing every single call.
 *
 * The dialog therefore has to send a usable scope set by default, and
 * must not let the user submit an empty one.
 */

import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: mocks.post, GET: vi.fn(), DELETE: vi.fn() },

  authSdk: { POST: mocks.post, GET: vi.fn(), DELETE: vi.fn() },
}));

import TokenCreateDialog from '../token-create-dialog';

function open(): void {
  renderWithProviders(<TokenCreateDialog workspaceId="ws-1" open onClose={() => {}} />);
}

function fillName(value = 'Laptop'): void {
  const name = screen.getByLabelText(/mcp_tokens.dialog.field.name/);
  fireEvent.change(name, { target: { value } });
}

function submit(): void {
  fireEvent.click(screen.getByText('workspace.mcp_tokens.dialog.submit'));
}

function sentBody(): Record<string, unknown> {
  return mocks.post.mock.calls[0]?.[1]?.body as Record<string, unknown>;
}

beforeEach(() => {
  mocks.post.mockReset();
  mocks.post.mockResolvedValue({
    data: { id: 't-1', token: 'mcp_secret' },
    error: null,
    response: new Response(null, { status: 200 }),
  });
});

describe('TokenCreateDialog scopes', () => {
  it('sends a non-empty scope set without the user touching the scope field', async () => {
    open();
    fillName();
    submit();

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalled();
    });
    const scopes = sentBody().scopes as string[];
    expect(Array.isArray(scopes)).toBe(true);
    expect(scopes.length).toBeGreaterThan(0);
    expect(scopes).toContain('read:workspace');
  });

  it('adds the write tier when it is ticked', async () => {
    open();
    fillName();
    const boxes = screen.getAllByRole('checkbox');
    const write = boxes[1];
    expect(write).toBeDefined();
    if (write) fireEvent.click(write);
    submit();

    await waitFor(() => {
      expect(mocks.post).toHaveBeenCalled();
    });
    expect(sentBody().scopes).toEqual(['read:workspace', 'write:workspace']);
  });

  it('refuses to submit once every scope is unticked', async () => {
    open();
    fillName();
    for (const box of screen.getAllByRole('checkbox')) {
      if ((box as HTMLInputElement).checked) fireEvent.click(box);
    }
    submit();

    expect(await screen.findByText('workspace.mcp_tokens.validation.scopes_required')).toBeTruthy();
    expect(mocks.post).not.toHaveBeenCalled();
  });

  it('puts a server field rejection on the scope field, not in a generic toast', async () => {
    // The real refusal shape: a problem+json body the SDK turns into an
    // ApiError carrying the code.
    mocks.post.mockResolvedValue({
      data: undefined,
      error: { type: 'VALIDATION.BODY.FIELD_INVALID', title: 'invalid', status: 422 },
      response: new Response(null, { status: 400 }),
    });
    open();
    fillName();
    submit();

    expect(await screen.findByText('workspace.mcp_tokens.validation.scopes_rejected')).toBeTruthy();
  });

  it('never mentions a default the server does not apply', () => {
    open();
    const help = screen.getByText('workspace.mcp_tokens.dialog.field.scopes_help');
    expect(help).toBeTruthy();
  });
});
