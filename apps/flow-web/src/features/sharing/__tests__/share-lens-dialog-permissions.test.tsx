/**
 * Publishing a lens puts a projection of the workspace's tasks on an
 * unauthenticated URL, so the API allows it only to the lens creator and to
 * workspace admins / owners; the same rule covers unpublishing. A viewer
 * outside that set must not be shown either control.
 *
 * Being read-only is not the same as being blank: a viewer who cannot manage
 * the share still sees whether one is live and can copy the link, which is
 * already public. Both halves are asserted so a dialog that rendered nothing
 * would fail.
 *
 * The third case is a lens that is public while this session does not hold
 * the token — the ordinary state, since the API returns the plaintext token
 * only in the publish response and stores a digest. The dialog must not offer
 * publish there (the API refuses it on an already-public lens) and must not
 * put a fabricated or empty URL on screen.
 */

import { render, screen } from '@testing-library/react';
import i18n from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import enSharing from '../../../../locales/en/sharing.json';

const publishMutate = vi.hoisted(() => vi.fn());
const unpublishMutate = vi.hoisted(() => vi.fn());

vi.mock('../api', () => ({
  usePublishLens: () => ({ mutateAsync: publishMutate, isPending: false }),
  useUnpublishLens: () => ({ mutateAsync: unpublishMutate, isPending: false }),
}));

import ShareLensDialog from '../share-lens-dialog';

function testI18n(): ReturnType<typeof i18n.createInstance> {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    defaultNS: 'sharing',
    ns: ['sharing'],
    resources: { en: { sharing: enSharing } },
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
  });
  return instance;
}

function renderDialog(opts: {
  canManage: boolean;
  publicToken: string | null;
  /** Defaults to whether a token is at hand, matching a just-published lens. */
  isPublic?: boolean;
}): void {
  render(
    <I18nextProvider i18n={testI18n()}>
      <ShareLensDialog
        workspaceId="ws-1"
        lensId="01920000-0000-7000-8000-000000001001"
        isPublic={opts.isPublic ?? opts.publicToken !== null}
        publicToken={opts.publicToken}
        canManage={opts.canManage}
        open
        onClose={() => {
          /* not under test */
        }}
      />
    </I18nextProvider>,
  );
}

describe('ShareLensDialog publish affordances', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('offers publish to a viewer who may manage a private lens', () => {
    renderDialog({ canManage: true, publicToken: null });
    expect(screen.getByText(enSharing.publish)).toBeTruthy();
  });

  it('offers unpublish to a viewer who may manage a published lens', () => {
    renderDialog({ canManage: true, publicToken: 'tok-1' });
    expect(screen.getByText(enSharing.unpublish)).toBeTruthy();
  });

  it('withholds publish from a viewer who may not manage the lens', () => {
    renderDialog({ canManage: false, publicToken: null });
    expect(screen.queryByText(enSharing.publish)).toBeNull();
    expect(screen.queryByText(enSharing.confirm_publish)).toBeNull();
    // The state is still reported rather than the dialog rendering nothing.
    expect(screen.getByText(enSharing.badge_private)).toBeTruthy();
  });

  it('withholds unpublish but still shows the live link to a viewer who may not manage', () => {
    renderDialog({ canManage: false, publicToken: 'tok-1' });
    expect(screen.queryByText(enSharing.unpublish)).toBeNull();
    expect(screen.queryByText(enSharing.confirm_unpublish)).toBeNull();
    expect(screen.getByText(enSharing.badge)).toBeTruthy();
    const link = screen.getByLabelText(enSharing.public_link) as HTMLInputElement;
    expect(link.value).toContain('/public/lenses/tok-1');
  });
});

describe('ShareLensDialog on a published lens whose token is not at hand', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('withholds publish from a manager and explains the link cannot be shown again', () => {
    renderDialog({ canManage: true, publicToken: null, isPublic: true });

    // Publishing is what the old token-only reading offered here, and the
    // API refuses it while `is_public` is true.
    expect(screen.queryByText(enSharing.publish)).toBeNull();
    expect(screen.queryByText(enSharing.confirm_publish)).toBeNull();

    expect(screen.getByText(enSharing.badge)).toBeTruthy();
    expect(screen.getByText(enSharing.link_unavailable)).toBeTruthy();
    expect(screen.getByText(enSharing.link_unavailable_hint)).toBeTruthy();

    // Withdrawing the share is still available to a manager.
    expect(screen.getByText(enSharing.unpublish)).toBeTruthy();
  });

  it('withholds both controls from a non-manager and still reports the lens is public', () => {
    renderDialog({ canManage: false, publicToken: null, isPublic: true });

    expect(screen.queryByText(enSharing.publish)).toBeNull();
    expect(screen.queryByText(enSharing.unpublish)).toBeNull();
    expect(screen.getByText(enSharing.badge)).toBeTruthy();
    expect(screen.getByText(enSharing.link_unavailable)).toBeTruthy();
    expect(screen.getByText(enSharing.close)).toBeTruthy();
  });

  it('renders no share URL at all rather than an empty or fabricated one', () => {
    renderDialog({ canManage: true, publicToken: null, isPublic: true });

    expect(screen.queryByLabelText(enSharing.public_link)).toBeNull();
    expect(screen.queryByText(enSharing.copy_link)).toBeNull();
    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).not.toContain('/public/lenses/');
  });
});
