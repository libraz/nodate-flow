/** Avatar upload / delete on the profile page (`POST` / `DELETE /me/avatar`). */

import { authStore } from '@nodate-flow/sdk';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sdkMocks = vi.hoisted(() => ({ post: vi.fn(), delete: vi.fn() }));
const toasterMock = vi.hoisted(() => ({ show: vi.fn() }));
const confirmMock = vi.hoisted(() => ({ confirmAction: vi.fn() }));

vi.mock('../../../lib/sdk', () => ({
  sdk: { POST: sdkMocks.post, DELETE: sdkMocks.delete },
  authSdk: { POST: sdkMocks.post, DELETE: sdkMocks.delete },
}));
vi.mock('@nodate-flow/ui/primitives/toast', () => ({
  toaster: toasterMock,
  default: () => null,
  ToastProvider: () => null,
}));
vi.mock('@nodate-flow/ui/primitives/confirm/action', () => confirmMock);

import AvatarUpload from '../avatar-upload';
import { newKeys } from './new-auth-keys';
import { withI18n } from './test-i18n';

const BASE_USER = {
  id: 'u1',
  email: 'a@b.test',
  displayName: 'Alice Doe',
  locale: 'en',
  timezone: 'UTC',
  country: 'US',
  themePreference: 'system',
  isInstanceAdmin: false,
  hasPassword: true,
} as const;

function meBody(avatarUrl: string | null): Record<string, unknown> {
  return { ...BASE_USER, avatarUrl, weekStart: 'mon' };
}

function ok(data: unknown) {
  return Promise.resolve({ data, error: null, response: new Response(null, { status: 200 }) });
}

function pickFile(): File {
  const file = new File(['x'], 'me.png', { type: 'image/png' });
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [file] } });
  return file;
}

beforeEach(() => {
  sdkMocks.post.mockReset();
  sdkMocks.delete.mockReset();
  toasterMock.show.mockReset();
  confirmMock.confirmAction.mockReset();
  authStore.getState().clearSession();
  authStore.getState().setSession('tok', { ...BASE_USER });
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:preview');
  globalThis.URL.revokeObjectURL = vi.fn();
});

describe('AvatarUpload', () => {
  it('uploads the picked file as multipart and syncs the session avatar', async () => {
    sdkMocks.post.mockImplementation(() => ok(meBody('https://cdn.test/a.png?v=1')));
    render(withI18n(<AvatarUpload avatarUrl={null} displayName="Alice Doe" />));

    const file = pickFile();

    await waitFor(() => {
      expect(authStore.getState().user?.avatarUrl).toBe('https://cdn.test/a.png?v=1');
    });
    const [path, init] = sdkMocks.post.mock.calls[0] as [
      string,
      { bodySerializer: () => FormData },
    ];
    expect(path).toBe('/me/avatar');
    expect(init.bodySerializer().get('file')).toBe(file);
    expect(toasterMock.show).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success' }));
  });

  it('shows the failure toast and keeps the session when the upload is refused', async () => {
    sdkMocks.post.mockResolvedValue({
      data: null,
      error: { type: 'AVATAR.TOO_LARGE', title: 'Too large', detail: 'x', status: 413 },
      response: new Response(null, { status: 413 }),
    });
    render(withI18n(<AvatarUpload avatarUrl={null} displayName="Alice Doe" />));

    pickFile();

    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalled();
    });
    const call = toasterMock.show.mock.calls[0]?.[0] as { tone: string; message: string };
    expect(call.tone).toBe('danger');
    expect(call.message.startsWith(newKeys.profile.avatar.upload_error)).toBe(true);
    expect(authStore.getState().user?.avatarUrl ?? null).toBeNull();
  });

  it('removes the avatar only after confirmation', async () => {
    sdkMocks.delete.mockImplementation(() => ok(meBody(null)));
    confirmMock.confirmAction.mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    render(withI18n(<AvatarUpload avatarUrl="https://cdn.test/a.png" displayName="Alice Doe" />));

    const remove = screen.getByRole('button', { name: 'Remove' });
    fireEvent.click(remove);
    await waitFor(() => {
      expect(confirmMock.confirmAction).toHaveBeenCalledTimes(1);
    });
    expect(sdkMocks.delete).not.toHaveBeenCalled();

    fireEvent.click(remove);
    await waitFor(() => {
      expect(sdkMocks.delete).toHaveBeenCalledWith('/me/avatar');
    });
    await waitFor(() => {
      expect(toasterMock.show).toHaveBeenCalledWith(expect.objectContaining({ tone: 'success' }));
    });
  });

  it('offers no Remove button when there is no avatar', () => {
    render(withI18n(<AvatarUpload avatarUrl={null} displayName="Alice Doe" />));
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
    expect(screen.getByRole('button', { name: newKeys.profile.avatar.upload_aria })).toBeTruthy();
  });
});
