import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('/invite/$token login returnTo preservation', () => {
  it('passes the invite path through /login and validates same-origin returnTo paths', () => {
    const invite = readFileSync(join(process.cwd(), 'src/routes/invite.$token.lazy.tsx'), 'utf8');
    const login = readFileSync(join(process.cwd(), 'src/routes/login.tsx'), 'utf8');

    expect(invite).toContain('search={{ returnTo: `/invite/$' + '{token}` }}');
    expect(login).toContain('validateSearch');
    // returnTo is validated by resolving it against this app's origin
    // (see the SDK's isSafeRedirect), not by prefix-matching the raw
    // string, which backslash-escaped paths slip past.
    expect(login).toContain('isSafeRedirect(raw, window.location.origin)');
    expect(login).toContain('new URL(returnTo ??');
    expect(login).toContain('redirect=');
  });
});
