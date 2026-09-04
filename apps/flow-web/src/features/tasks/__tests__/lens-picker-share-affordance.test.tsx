/**
 * Publishing a saved view is the only route from the product to the public
 * lens page, and it runs through the picker: a share control on each row,
 * gated exactly as the delete control is, because the API gates publish,
 * unpublish and delete on the same rule.
 *
 * Two things are asserted in both directions. The control follows the gate —
 * a member who may not manage a lens is not offered a button the API would
 * refuse, and everyone else is. The "public" marker does not follow the gate
 * at all: it is the only way a member who cannot withdraw a share can see
 * that one exists, so it is asserted against a viewer who has no controls.
 *
 * The wiring itself is the point, so the real dialog is mounted (only the
 * publish/unpublish mutations are stubbed) and a refusal from publish is
 * driven through it — the query client does not throw, so a mutation whose
 * failure is not handled would leave the user staring at an unchanged dialog.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import i18n from 'i18next';
import type { ReactNode } from 'react';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enCommon from '../../../../locales/en/common.json';
import enErrors from '../../../../locales/en/errors.json';
import enSharing from '../../../../locales/en/sharing.json';

/* ── Shared handles (vi.mock factories are hoisted) ───────────── */

const state = vi.hoisted(() => ({
  lenses: [] as unknown[],
  workspaceRole: 'member',
}));
const toastShow = vi.hoisted(() => vi.fn());
const publishMutateAsync = vi.hoisted(() => vi.fn());
const unpublishMutateAsync = vi.hoisted(() => vi.fn());

/* ── Mocks ────────────────────────────────────────────────────── */

// The popover owns positioning and a focus trap; the panel renders inline so
// the rows are visible without driving the trigger.
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
  useDeleteLens: () => ({ mutateAsync: vi.fn() }),
  useCreateLens: () => ({ mutateAsync: vi.fn() }),
  lensesKeys: {
    all: ['lenses'],
    list: (workspaceId: string, projectId?: string) => ['lenses', workspaceId, projectId ?? ''],
  },
}));

vi.mock('../../sharing/api', () => ({
  usePublishLens: () => ({ mutateAsync: publishMutateAsync, isPending: false }),
  useUnpublishLens: () => ({ mutateAsync: unpublishMutateAsync, isPending: false }),
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
    isPublic: false,
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
    ns: ['common', 'errors', 'sharing'],
    resources: { en: { common: enCommon, errors: enErrors, sharing: enSharing } },
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
  return instance;
}

/** Render the picker as `signedInAs`, holding the role in `state`. */
function renderAs(signedInAs: string): QueryClient {
  authStore.getState().setSession('test-token', makeUser(signedInAs));
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={testI18n()}>
        <LensPicker workspaceId="ws-1" projectId="p-1" />
      </I18nextProvider>
    </QueryClientProvider>,
  );
  return client;
}

const shareLabel = enCommon.tasks.lens.share;
const deleteLabel = enCommon.tasks.lens.delete;
const publicBadge = enCommon.tasks.lens.public_badge;

describe('LensPicker share affordance', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authStore.getState().clearSession();
    state.lenses = [makeLens()];
    state.workspaceRole = 'member';
  });

  it('offers share alongside delete to the lens creator', () => {
    renderAs(CREATOR_ID);
    expect(screen.getByLabelText(shareLabel)).toBeTruthy();
    expect(screen.getByLabelText(deleteLabel)).toBeTruthy();
  });

  it('offers share to a workspace admin who did not create the lens', () => {
    state.workspaceRole = 'admin';
    renderAs(MEMBER_ID);
    expect(screen.getByLabelText(shareLabel)).toBeTruthy();
  });

  it('withholds share from a member who is neither creator nor admin', () => {
    renderAs(MEMBER_ID);
    // The row is still listed and applicable — only the write controls go.
    expect(screen.getByText('Roadmap')).toBeTruthy();
    expect(screen.queryByLabelText(shareLabel)).toBeNull();
    expect(screen.queryByLabelText(deleteLabel)).toBeNull();
  });

  it('gates share on exactly the rows delete is gated on', () => {
    state.lenses = [
      makeLens(),
      makeLens({
        id: '01920000-0000-7000-8000-000000001002',
        name: 'Mine',
        creatorId: MEMBER_ID,
      }),
    ];
    renderAs(MEMBER_ID);
    expect(screen.getAllByLabelText(shareLabel)).toHaveLength(1);
    expect(screen.getByLabelText(shareLabel).closest('li')?.textContent).toContain('Mine');
  });
});

describe('LensPicker public marker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authStore.getState().clearSession();
    state.workspaceRole = 'member';
  });

  it('marks a published lens for a member who cannot manage it', () => {
    state.lenses = [makeLens({ isPublic: true })];
    renderAs(MEMBER_ID);
    // No controls for this viewer, so the marker is all they get.
    expect(screen.queryByLabelText(shareLabel)).toBeNull();
    expect(screen.getByText(publicBadge)).toBeTruthy();
  });

  it('leaves a private lens unmarked', () => {
    state.lenses = [makeLens()];
    renderAs(CREATOR_ID);
    expect(screen.getByText('Roadmap')).toBeTruthy();
    expect(screen.queryByText(publicBadge)).toBeNull();
  });

  it('marks only the published row when the list mixes both', () => {
    state.lenses = [
      makeLens(),
      makeLens({ id: '01920000-0000-7000-8000-000000001002', name: 'Shared', isPublic: true }),
    ];
    renderAs(CREATOR_ID);
    expect(screen.getAllByText(publicBadge)).toHaveLength(1);
    expect(screen.getByText(publicBadge).closest('li')?.textContent).toContain('Shared');
  });
});

describe('LensPicker share dialog wiring', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authStore.getState().clearSession();
    state.lenses = [makeLens()];
    state.workspaceRole = 'member';
  });

  it('mounts the share dialog for the row that was clicked', () => {
    renderAs(CREATOR_ID);
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.click(screen.getByLabelText(shareLabel));

    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).toContain(enSharing.title);
    // A private lens gets the publish branch, not a link.
    expect(screen.getByText(enSharing.publish)).toBeTruthy();
    expect(screen.queryByLabelText(enSharing.public_link)).toBeNull();
  });

  it('opens the already-published branch for a lens that is public', () => {
    state.lenses = [makeLens({ isPublic: true })];
    renderAs(CREATOR_ID);

    fireEvent.click(screen.getByLabelText(shareLabel));

    // No token is at hand, so the dialog must not offer publish here.
    expect(screen.queryByText(enSharing.publish)).toBeNull();
    expect(screen.getByText(enSharing.link_unavailable)).toBeTruthy();
    expect(screen.getByText(enSharing.unpublish)).toBeTruthy();
  });

  it('shows the minted link and refreshes the lens list after publishing', async () => {
    publishMutateAsync.mockResolvedValueOnce({ publicToken: 'tok-fresh' });
    const client = renderAs(CREATOR_ID);
    const invalidate = vi.spyOn(client, 'invalidateQueries');

    fireEvent.click(screen.getByLabelText(shareLabel));
    fireEvent.click(screen.getByText(enSharing.publish));

    await waitFor(() => {
      expect(publishMutateAsync).toHaveBeenCalledTimes(1);
    });
    const link = (await screen.findByLabelText(enSharing.public_link)) as HTMLInputElement;
    expect(link.value).toContain('/public/lenses/tok-fresh');
    // Otherwise the row keeps claiming the view is private.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['lenses', 'ws-1', 'p-1'] });
  });

  it('surfaces a publish refusal instead of leaving the dialog unchanged', async () => {
    const refusal = new ApiError(
      'WS.MEMBER.ROLE_DENIED',
      'only the creator or a workspace admin may publish this view',
      403,
    );
    publishMutateAsync.mockRejectedValueOnce(refusal);
    renderAs(CREATOR_ID);

    fireEvent.click(screen.getByLabelText(shareLabel));
    fireEvent.click(screen.getByText(enSharing.publish));

    await waitFor(() => {
      expect(toastShow).toHaveBeenCalledTimes(1);
    });
    const call = toastShow.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(call.tone).toBe('danger');
    expect([refusal.message, enErrors['WS.MEMBER.ROLE_DENIED']]).toContain(call.message);
    // No link is invented on a failed publish.
    expect(screen.queryByLabelText(enSharing.public_link)).toBeNull();
  });
});
