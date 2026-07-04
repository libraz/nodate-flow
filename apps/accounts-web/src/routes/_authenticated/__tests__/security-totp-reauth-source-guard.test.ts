import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('accounts security TOTP reauth source guard', () => {
  const source = readFileSync(resolve(__dirname, '../security.tsx'), 'utf-8');

  it('sends the current password for TOTP enroll and confirm', () => {
    expect(source).toContain("sdk.POST('/me/totp/enroll', { body: { password } })");
    expect(source).toContain("sdk.POST('/me/totp/confirm', { body: { code, password } })");
    expect(source).toContain("errCode === 'AUTH.PASSWORD.CURRENT_MISMATCH'");
    expect(source).toContain('onConfirm={(code, password) =>');
  });
});
