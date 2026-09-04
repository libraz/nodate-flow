/**
 * The API gates every write to a saved lens — update, delete, publish and
 * unpublish — to the lens creator and to workspace admins / owners. A
 * workspace member who is neither gets `WS.MEMBER.ROLE_DENIED`.
 *
 * The picker therefore has to answer the same question the server does
 * before it offers the delete control, and it has to keep answering it for
 * the cases where the affordance is legitimately there: hiding a button for
 * everyone would satisfy a negative-only test while breaking the product.
 * Both directions are asserted below, against the same lens list.
 *
 * The refusal is the actual boundary, so the last case drives a 403 through
 * a picker that did offer the control (the role can change, or another tab
 * can act first) and asserts the user is told.
 */

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enCommon from '../../../../locales/en/common.json';
import enErrors from '../../../../locales/en/errors.json';

/* ── Shared handles (vi.mock factories are hoisted) ───────────── */

const state = vi.hoisted(() => ({
  lenses: [] as unknown[],
  workspaceRole: 'member',
}));
const toastShow = vi.hoisted(() => vi.fn());
const deleteMutateAsync = vi.hoisted(() => vi.fn());
const createMutateAsync = vi.hoisted(() => vi.fn());

/* ── Mocks ────────────────────────────────────────────────────── */

// The popover owns positioning and a focus trap; neither is what this
// test is about, so the panel renders inline and the list is always
// visible.
vi.mock('@nodate-flow/ui/primitives/popover', () => ({
  default: ({ children, content }: { children: ReactNode; content: ReactNode }) => (
    <>
      {children}
      {content}
    </>
  ),
}));

vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: { show: toastShow },
}));

vi.mock('../lens-api', () => ({
  useLensesQuery: () => ({ data: state.lenses }),
  useDeleteLens: () => ({ mutateAsync: deleteMutateAsync }),
  useCreateLens: () => ({ mutateAsync: createMutateAsync }),
}));

vi.mock('../../workspaces/api', () => ({
  useWorkspaceQuery: () => ({ data: { id: 'ws-1', name: 'Acme', role: state.workspaceRole } }),
}));

/* ── Imports under test (after the mocks) ─────────────────────── */

import { ApiError } from '../../../lib/api-error';
import { type AuthUser, authStore } from '../../auth/auth-store';
import type { LensDto } from '../lens-api';
import LensPicker from '../lens-picker';

/* ── Fixtures ─────────────────────────────────────────────────── */

const CREATOR_ID = '01920000-0000-7000-8000-0000000000c1';
const ADMIN_ID = '01920000-0000-7000-8000-0000000000a1';
const MEMBER_ID = '01920000-0000-7000-8000-0000000000b1';

function makeUser(id: string): AuthUser {
  return {
    id,
    email: `${id}@example.test`,
    displayName: 'Tester',
    locale: 'en',
    timezone: 'UTC',
    country: 'JP',
    themePreference: 'aurora-light',
    isInstanceAdmin: false,
  };
}

function makeLens(overrides: Partial<LensDto> = {}): LensDto {
  return {
    id: '01920000-0000-7000-8000-000000001001',
    creatorId: CREATOR_ID,
    creatorDisplayName: 'Creator',
    name: 'Roadmap',
    filter: {},
    sort: [],
    groupBy: null,
    isDefault: false,
    sortWeight: 0,
    createdAt: 1_700_000_000,
    ...overrides,
  };
}

/* ── Test i18n ────────────────────────────────────────────────── */

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'common',
    ns: ['common', 'errors'],
    resources: { en: { common: enCommon, errors: enErrors } },
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
  return instance;
}

/** Render the picker as `signedInAs`, holding the role in `state`. */
function renderAs(signedInAs: string): void {
  authStore.getState().setSession('test-token', makeUser(signedInAs));
  render(
    <I18nextProvider i18n={testI18n()}>
      <LensPicker workspaceId="ws-1" projectId="p-1" />
    </I18nextProvider>,
  );
}

const deleteLabel = enCommon.tasks.lens.delete;

describe('LensPicker write affordances', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authStore.getState().clearSession();
    state.lenses = [makeLens()];
    state.workspaceRole = 'member';
  });

  it('offers delete to the lens creator', () => {
    renderAs(CREATOR_ID);
    expect(screen.getByText('Roadmap')).toBeTruthy();
    expect(screen.getByLabelText(deleteLabel)).toBeTruthy();
  });

  it('offers delete to a workspace admin who did not create the lens', () => {
    state.workspaceRole = 'admin';
    renderAs(ADMIN_ID);
    expect(screen.getByText('Roadmap')).toBeTruthy();
    expect(screen.getByLabelText(deleteLabel)).toBeTruthy();
  });

  it('offers delete to a workspace owner who did not create the lens', () => {
    state.workspaceRole = 'owner';
    renderAs(ADMIN_ID);
    expect(screen.getByLabelText(deleteLabel)).toBeTruthy();
  });

  it('does not offer delete to a member who is neither creator nor admin', () => {
    renderAs(MEMBER_ID);
    // The row is there — this is a withheld control, not an empty panel.
    expect(screen.getByText('Roadmap')).toBeTruthy();
    expect(screen.queryByLabelText(deleteLabel)).toBeNull();
  });

  it('withholds delete on a lens the viewer did not create while offering it on their own', () => {
    state.lenses = [
      makeLens(),
      makeLens({
        id: '01920000-0000-7000-8000-000000001002',
        name: 'Mine',
        creatorId: MEMBER_ID,
      }),
    ];
    renderAs(MEMBER_ID);
    expect(screen.getAllByLabelText(deleteLabel)).toHaveLength(1);
    const remaining = screen.getByLabelText(deleteLabel);
    expect(remaining.closest('li')?.textContent).toContain('Mine');
  });
});

describe('LensPicker refusal handling', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authStore.getState().clearSession();
    state.lenses = [makeLens()];
    state.workspaceRole = 'member';
  });

  it('surfaces a 403 that arrives despite the gate instead of swallowing it', async () => {
    const refusal = new ApiError(
      'WS.MEMBER.ROLE_DENIED',
      'only the creator or a workspace admin may delete this view',
      403,
    );
    deleteMutateAsync.mockRejectedValueOnce(refusal);

    renderAs(CREATOR_ID);
    fireEvent.click(screen.getByLabelText(deleteLabel));

    await waitFor(() => {
      expect(toastShow).toHaveBeenCalledTimes(1);
    });
    const call = toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(call.tone).toBe('danger');
    // Built from the caught refusal: either the API's own detail or the
    // catalog sentence for its code — never the generic "delete failed".
    expect([refusal.message, enErrors['WS.MEMBER.ROLE_DENIED']]).toContain(call.message);
    expect(call.message).not.toBe(enCommon.tasks.lens.delete_failed);
  });

  it('shows nothing when the delete succeeds', async () => {
    deleteMutateAsync.mockResolvedValueOnce(undefined);

    renderAs(CREATOR_ID);
    fireEvent.click(screen.getByLabelText(deleteLabel));

    await waitFor(() => {
      expect(deleteMutateAsync).toHaveBeenCalledTimes(1);
    });
    expect(toastShow).not.toHaveBeenCalled();
  });
});
