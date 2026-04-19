import { describe, expect, it } from 'vitest';

describe('SDK public exports', () => {
  it('exports auth-store symbols', async () => {
    const mod = await import('../index');
    expect(mod.authStore).toBeDefined();
    expect(mod.useAuth).toBeDefined();
    expect(mod.selectAccessToken).toBeDefined();
    expect(mod.selectUser).toBeDefined();
    expect(mod.selectIsAuthenticated).toBeDefined();
  });

  it('exports API error utilities', async () => {
    const mod = await import('../index');
    expect(mod.ApiError).toBeDefined();
    expect(mod.toApiError).toBeDefined();
  });

  it('exports query client utilities', async () => {
    const mod = await import('../index');
    expect(mod.createQueryClient).toBeDefined();
    expect(mod.queryClient).toBeDefined();
  });

  it('exports QueryProvider component', async () => {
    const mod = await import('../index');
    expect(mod.QueryProvider).toBeDefined();
  });

  it('exports I18nProvider component', async () => {
    const mod = await import('../index');
    expect(mod.I18nProvider).toBeDefined();
  });

  it('exports refresh utilities (pre-existing)', async () => {
    const mod = await import('../index');
    expect(mod.createTokenRefresher).toBeDefined();
    expect(mod.createRefreshMiddleware).toBeDefined();
  });

  it('exports createClient', async () => {
    const mod = await import('../index');
    expect(mod.createClient).toBeDefined();
  });
});
