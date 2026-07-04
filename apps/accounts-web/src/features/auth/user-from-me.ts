import type { AuthUser, components } from '@nodate-flow/sdk';

export type MeResponse = components['schemas']['MeBody'];

export function userFromMe(me: MeResponse): AuthUser {
  return {
    id: me.id,
    email: me.email,
    displayName: me.displayName,
    locale: me.locale,
    timezone: me.timezone,
    country: me.country,
    themePreference: me.themePreference,
    isInstanceAdmin: me.isInstanceAdmin,
    avatarUrl: me.avatarUrl ?? null,
  };
}
