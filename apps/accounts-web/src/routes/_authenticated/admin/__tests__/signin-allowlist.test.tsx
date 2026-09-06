/**
 * The sign-in allowlist screen has to say what its own emptiness means.
 *
 * Every other admin table reads "none of these exist yet". This one reads
 * "everyone is admitted", and it hides a second half kept in the
 * environment that no row here can show or remove. An administrator who
 * reads the table the usual way concludes the opposite of the truth in
 * both directions, so the two sentences are asserted as rendered output
 * rather than left to whoever writes the next revision of the copy.
 *
 * The rest covers what a wrong render would cost: a withdrawn entry
 * mixed in with the live ones reads as access that is still granted, and
 * a refused write that says nothing leaves the operator believing an
 * address was admitted — or removed — when it was not.
 */

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import i18n from 'i18next';
import ICU from 'i18next-icu';
import type { ReactElement, ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import enAdmin from '../../../../../locales/en/admin.json';

const sdkMocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));

const confirmMock = vi.hoisted(() => ({ fn: vi.fn() }));
const toasterMock = vi.hoisted(() => ({ show: vi.fn() }));

vi.mock('../../../../lib/sdk', () => ({
  sdk: {
    GET: sdkMocks.get,
    POST: sdkMocks.post,
    DELETE: sdkMocks.delete,
  },
}));

vi.mock('@nodate-flow/ui/primitives/confirm/action', () => ({
  confirmAction: confirmMock.fn,
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  ToastProvider: () => null,
}));

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => () => ({ options: {} }),
}));

const { SignInAllowlistPage } = await import('../signin-allowlist');

const copy = enAdmin.signin_allowlist;

interface Entry {
  id: string;
  kind: 'domain' | 'email';
  value: string;
  enabled: boolean;
  notes?: string;
  addedById?: string;
  addedByDisplayName?: string;
  createdAt: number;
  updatedAt?: number;
}

const liveDomain: Entry = {
  id: 'e-1',
  kind: 'domain',
  value: 'example.test',
  enabled: true,
  notes: 'head office',
  addedById: 'u-1',
  addedByDisplayName: 'Admin One',
  createdAt: 1700000000,
};

const withdrawnAddress: Entry = {
  id: 'e-2',
  kind: 'email',
  value: 'contractor@vendor.test',
  enabled: false,
  createdAt: 1700000100,
  updatedAt: 1700000200,
};

function buildI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      defaultNS: 'admin',
      ns: ['admin'],
      resources: { en: { admin: enAdmin } },
      interpolation: { escapeValue: false },
      react: { useSuspense: false },
    });
  return instance;
}

function mount(): void {
  const testI18n = buildI18n();
  function Wrapper({ children }: { children: ReactNode }): ReactElement {
    return <I18nextProvider i18n={testI18n}>{children}</I18nextProvider>;
  }
  render(<SignInAllowlistPage />, { wrapper: Wrapper });
}

/** Every list read answers with `entries`, so a refetch is observable by count. */
function primeList(entries: Entry[]): void {
  sdkMocks.get.mockImplementation(async () => ({
    data: { items: entries, total: entries.length },
    error: null,
    response: new Response(null, { status: 200 }),
  }));
}

beforeAll(() => {
  if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => undefined,
        removeListener: () => undefined,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
        dispatchEvent: () => false,
      }),
    });
  }
});

beforeEach(() => {
  sdkMocks.get.mockReset();
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
  confirmMock.fn.mockReset();
  toasterMock.show.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('sign-in allowlist', () => {
  it('says an empty list admits everyone, and names the environment half it cannot show', async () => {
    primeList([]);

    mount();

    expect(await screen.findByText(copy.open_title)).not.toBeNull();
    expect(screen.getByText(copy.open_body)).not.toBeNull();
    expect(screen.getByText(copy.env_notice)).not.toBeNull();
    // Nothing to act on, so no table claims otherwise.
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('says the same when every entry has been withdrawn', async () => {
    // The list is not empty, but nothing in it admits anybody, so sign-in
    // is open again and the screen has to say so.
    primeList([withdrawnAddress]);

    mount();

    expect(await screen.findByText(copy.open_title)).not.toBeNull();
    expect(screen.getByRole('button', { name: copy.restore })).not.toBeNull();
  });

  it('keeps a withdrawn entry out of the active table and offers to restore it', async () => {
    primeList([liveDomain, withdrawnAddress]);

    mount();

    await screen.findByText(`@${liveDomain.value}`);
    const [activeTable, withdrawnTable] = screen.getAllByRole('table');
    expect(withdrawnTable).not.toBeUndefined();

    // The live entry is withdrawable; the withdrawn one is restorable, and
    // neither appears in the other's table.
    expect(activeTable?.textContent).toContain(liveDomain.value);
    expect(activeTable?.textContent).not.toContain(withdrawnAddress.value);
    expect(withdrawnTable?.textContent).toContain(withdrawnAddress.value);
    expect(withdrawnTable?.textContent).not.toContain(liveDomain.value);
    expect(screen.getByText(copy.status_withdrawn)).not.toBeNull();

    // A domain admits a whole organisation and an address admits one
    // person; each row carries the label rather than leaving the reader to
    // infer the kind from the raw value.
    expect(within(activeTable as HTMLElement).getByText(copy.kind_domain)).not.toBeNull();
    expect(within(withdrawnTable as HTMLElement).getByText(copy.kind_email)).not.toBeNull();
  });

  it('reports a refused add with the code the API returned, and keeps what was typed', async () => {
    primeList([]);
    sdkMocks.post.mockResolvedValue({
      data: null,
      error: { type: 'VALIDATION.BODY.FIELD_INVALID', title: 'Unprocessable', status: 422 },
      response: new Response(null, { status: 422 }),
    });

    mount();

    const value = await screen.findByLabelText(copy.add_domain_label);
    await userEvent.type(value, 'example.test');
    await userEvent.click(screen.getByRole('button', { name: copy.add_submit }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('VALIDATION.BODY.FIELD_INVALID');
    // The refused input is still there to correct, not silently discarded.
    expect((value as HTMLInputElement).value).toBe('example.test');
  });

  it('clears the form and re-reads the list once the add is accepted', async () => {
    primeList([]);
    sdkMocks.post.mockResolvedValue({
      data: { ...liveDomain },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    mount();

    const value = await screen.findByLabelText(copy.add_domain_label);
    const readsBeforeAdd = sdkMocks.get.mock.calls.length;
    await userEvent.type(value, 'example.test');
    await userEvent.click(screen.getByRole('button', { name: copy.add_submit }));

    await waitFor(() => {
      expect(sdkMocks.get.mock.calls.length).toBeGreaterThan(readsBeforeAdd);
    });
    await waitFor(() => {
      expect((value as HTMLInputElement).value).toBe('');
    });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows both kinds at once, and switching kind relabels the value it will send', async () => {
    // Kind decides what the field beside it means -- a whole address or a
    // bare domain -- so both options stay on screen and the switch has to
    // carry through to the label, the placeholder and the request body.
    primeList([]);
    sdkMocks.post.mockResolvedValue({
      data: { ...withdrawnAddress, enabled: true },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    mount();

    const group = await screen.findByRole('radiogroup', { name: copy.add_kind_label });
    const [domain, email] = within(group).getAllByRole('radio');
    expect(domain?.getAttribute('aria-checked')).toBe('true');
    expect(email?.getAttribute('aria-checked')).toBe('false');

    await userEvent.click(email as HTMLElement);
    expect(email?.getAttribute('aria-checked')).toBe('true');
    expect(domain?.getAttribute('aria-checked')).toBe('false');

    const value = await screen.findByLabelText(copy.add_email_label);
    expect(value.getAttribute('placeholder')).toBe(copy.add_email_placeholder);
    await userEvent.type(value, 'someone@vendor.test');
    await userEvent.click(screen.getByRole('button', { name: copy.add_submit }));

    await waitFor(() => {
      expect(sdkMocks.post).toHaveBeenCalled();
    });
    expect(sdkMocks.post.mock.calls[0]?.[1]?.body).toEqual({
      kind: 'email',
      value: 'someone@vendor.test',
    });
  });

  it('names the kind segment and the field it relabels differently', async () => {
    // A segment names itself with its own label, so a value field named
    // after the kind it holds would leave the form with two controls
    // called the same thing -- one of which decides what the other means.
    // Every lookup here is a getBy*, which throws on a second match, so
    // each name has to reach exactly one control.
    primeList([]);

    mount();

    const group = await screen.findByRole('radiogroup', { name: copy.add_kind_label });
    const [domain, email] = within(group).getAllByRole('radio');

    const field = screen.getByRole('textbox', { name: copy.add_domain_label });
    expect(screen.getByLabelText(copy.add_domain_label)).toBe(field);
    expect(screen.getByLabelText(copy.kind_domain)).toBe(domain);

    // Switching kind relabels that same field, and the new name is its own
    // as well.
    await userEvent.click(email as HTMLElement);
    expect(screen.getByLabelText(copy.add_email_label)).toBe(field);
    expect(screen.getByLabelText(copy.kind_email)).toBe(email);
  });

  it('warns that an address is rewritten before it is stored, not only a domain', async () => {
    // The server lower-cases and trims whichever kind is sent, and takes a
    // leading @ off a domain on top of that. A hint that named only
    // domains would leave an administrator expecting Person@Vendor.test to
    // be kept as typed, and reading the row it comes back as as a
    // different entry from the one they added.
    primeList([]);

    mount();

    const hint = (await screen.findByText(copy.add_hint)).textContent ?? '';
    expect(hint.toLowerCase()).toContain(copy.kind_domain.toLowerCase());
    expect(hint.toLowerCase()).toContain(copy.kind_email.toLowerCase());
    expect(hint).toMatch(/lower ?case/i);
  });

  it('lets the keyboard reach and change the kind without a pointer', async () => {
    // A roving tabindex means only the selected segment is tabbable, so the
    // group is one Tab stop and the arrow keys move the selection.
    primeList([]);

    mount();

    const group = await screen.findByRole('radiogroup', { name: copy.add_kind_label });
    const [domain, email] = within(group).getAllByRole('radio');
    expect(domain?.getAttribute('tabindex')).toBe('0');
    expect(email?.getAttribute('tabindex')).toBe('-1');

    (domain as HTMLElement).focus();
    await userEvent.keyboard('{ArrowRight}');
    expect(email?.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(email);

    await userEvent.keyboard('{ArrowLeft}');
    expect(domain?.getAttribute('aria-checked')).toBe('true');
    // Tabbing on from the group lands on the value field, not on the
    // unselected segment.
    await userEvent.tab();
    expect(document.activeElement).toBe(await screen.findByLabelText(copy.add_domain_label));
  });

  it('reports a refused withdrawal instead of dropping the entry off the screen', async () => {
    primeList([liveDomain]);
    confirmMock.fn.mockResolvedValue(true);
    sdkMocks.delete.mockResolvedValue({
      data: null,
      error: { type: 'INSTANCE.OAUTH_ALLOWLIST.NOT_FOUND', title: 'Not Found', status: 404 },
      response: new Response(null, { status: 404 }),
    });

    mount();

    await userEvent.click(await screen.findByRole('button', { name: copy.withdraw }));

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const toast = toasterMock.show.mock.calls[0]?.[0];
    expect(toast?.tone).toBe('danger');
    expect(String(toast?.message)).toContain('INSTANCE.OAUTH_ALLOWLIST.NOT_FOUND');
    // The entry it refused to withdraw is still listed as admitting people.
    expect(screen.getByText(`@${liveDomain.value}`)).not.toBeNull();
  });

  it('restores a withdrawn entry by re-sending its own kind and value', async () => {
    primeList([withdrawnAddress]);
    sdkMocks.post.mockResolvedValue({
      data: { ...withdrawnAddress, enabled: true },
      error: null,
      response: new Response(null, { status: 200 }),
    });

    mount();

    await userEvent.click(await screen.findByRole('button', { name: copy.restore }));

    await waitFor(() => {
      expect(sdkMocks.post).toHaveBeenCalled();
    });
    const body = sdkMocks.post.mock.calls[0]?.[1]?.body;
    expect(body).toEqual({ kind: withdrawnAddress.kind, value: withdrawnAddress.value });
  });
});
