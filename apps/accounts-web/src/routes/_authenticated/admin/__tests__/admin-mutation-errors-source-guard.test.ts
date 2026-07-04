import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('admin mutation error handling source guard', () => {
  const users = readFileSync(resolve(__dirname, '../users_.$userId.tsx'), 'utf-8');
  const workspaces = readFileSync(resolve(__dirname, '../workspaces_.$wsId.tsx'), 'utf-8');
  const admins = readFileSync(resolve(__dirname, '../admins.tsx'), 'utf-8');
  const settings = readFileSync(resolve(__dirname, '../settings.tsx'), 'utf-8');

  it('checks user detail mutation errors before refetching or mutating local state', () => {
    expect(users).toContain("const { error: err } = await sdk.PATCH('/admin/users/{userId}'");
    expect(users).toContain(
      "const { error: err } = await sdk.DELETE('/admin/instance-admins/{userId}'",
    );
    expect(users).toContain("const { error: err } = await sdk.POST('/admin/instance-admins'");
    expect(users).toContain(
      "const { error: err } = await sdk.DELETE('/admin/sessions/{sessionId}'",
    );
    expect(users).toContain('message: code ? `$' + "{t('errors.generic')} ($" + '{code})`');
  });

  it('checks workspace, admins, and settings mutation errors', () => {
    expect(workspaces).toContain("sdk.PATCH('/admin/workspaces/{wsId}'");
    expect(workspaces).toContain('message: code ? `$' + "{t('errors.generic')} ($" + '{code})`');
    expect(admins).toContain(
      "const { error: err } = await sdk.DELETE('/admin/instance-admins/{userId}'",
    );
    expect(admins).toContain("setError(t('errors.generic'))");
    expect(settings).toContain("const { error: err } = await sdk.PATCH('/admin/settings'");
    expect(settings).toContain('formatSettingsSaveError');
    expect(settings).toContain("typeof err.detail === 'string'");
  });
});
