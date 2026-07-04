import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('accounts SDK typing', () => {
  it('exports the shared typed SDK instead of an erased AnySdk wrapper', () => {
    const source = readFileSync(join(process.cwd(), 'src/lib/sdk.ts'), 'utf8');

    expect(source).toContain('type NodateFlowClient');
    expect(source).toContain('export const sdk: NodateFlowClient');
    expect(source).not.toContain('AnySdk');
  });

  it('uses the OpenAPI userId path parameter for instance-admin revoke calls', () => {
    const adminsSource = readFileSync(
      join(process.cwd(), 'src/routes/_authenticated/admin/admins.tsx'),
      'utf8',
    );
    const userDetailSource = readFileSync(
      join(process.cwd(), 'src/routes/_authenticated/admin/users_.$userId.tsx'),
      'utf8',
    );

    expect(adminsSource).toContain("'/admin/instance-admins/{userId}'");
    expect(userDetailSource).toContain("'/admin/instance-admins/{userId}'");
    expect(`${adminsSource}\n${userDetailSource}`).not.toContain(
      '/admin/instance-admins/{adminId}',
    );
  });
});
