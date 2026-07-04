import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

describe('/invite/$token branded error boundary', () => {
  it('registers a route-local branded errorComponent', () => {
    const src = readFileSync(join(process.cwd(), 'src/routes/invite.$token.tsx'), 'utf8');

    expect(src).toContain('errorComponent: InviteErrorComponent');
    expect(src).toContain('<AuthCard>');
    expect(src).toContain('WS.WORKSPACE_INVITE.EXPIRED');
    expect(src).toContain('WS.WORKSPACE_INVITE.EXHAUSTED');
    expect(src).toContain('isNetworkError(error)');
  });
});
