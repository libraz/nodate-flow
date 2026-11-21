/**
 * Avatar endpoints (`POST` / `DELETE /me/avatar`).
 *
 * The upload is multipart, so the SDK's JSON serializer is replaced with a
 * `FormData` one; openapi-fetch then leaves `Content-Type` to the browser so
 * the boundary is set.
 */

import type { components } from '@nodate-flow/sdk';

import { apiRequest } from '../../lib/api';

export type Me = components['schemas']['MeBody'];

/** Upload a profile picture; resolves to the updated `/me` body. */
export async function uploadAvatar(file: File): Promise<Me> {
  const data = await apiRequest(
    (client) =>
      client.POST('/me/avatar', {
        // The generated type models the binary part as a string.
        body: { file: file as unknown as string },
        bodySerializer: () => {
          const form = new FormData();
          form.append('file', file);
          return form;
        },
      }),
    'Failed to upload the profile picture',
  );
  return data as Me;
}

/** Remove the profile picture; resolves to the updated `/me` body. */
export async function deleteAvatar(): Promise<Me> {
  const data = await apiRequest(
    (client) => client.DELETE('/me/avatar'),
    'Failed to remove the profile picture',
  );
  return data as Me;
}
