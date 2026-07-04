import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('TOTP enrollment reauth source guard', () => {
  const api = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');
  const panel = readFileSync(resolve(__dirname, '../totp-panel.tsx'), 'utf-8');
  const openapi = readFileSync(
    resolve(__dirname, '../../../../../../packages/sdk/src/openapi.ts'),
    'utf-8',
  );

  it('sends the current password for enroll and confirm', () => {
    expect(api).toContain("sdk.POST('/me/totp/enroll', { body: { password } })");
    expect(api).toContain("sdk.POST('/me/totp/confirm', { body: { code, password } })");
    expect(panel).toContain('confirm.mutateAsync({ code, password })');
    expect(panel).toContain('<StartEnrollmentForm status={status} onEnroll={handleEnroll}');
  });

  it('keeps generated SDK types aligned with the password body contract', () => {
    expect(openapi).toContain('TotpEnrollInputBody');
    expect(openapi).toContain('password: string;');
    expect(openapi).toContain('"application/json": components["schemas"]["TotpEnrollInputBody"]');
    expect(openapi).toContain('"application/json": components["schemas"]["TotpConfirmInputBody"]');
  });
});
