/**
 * Global teardown for flow-web E2E tests.
 *
 * Logs out shared tenants and removes the temp JSON file.
 */

import { existsSync, readFileSync, unlinkSync } from 'node:fs';

import { type SharedTenants, TENANTS_PATH } from './global-setup';
import { cleanupTenant } from './tenant';

async function globalTeardown(): Promise<void> {
  if (!existsSync(TENANTS_PATH)) return;

  try {
    const tenants = JSON.parse(readFileSync(TENANTS_PATH, 'utf-8')) as SharedTenants;
    await cleanupTenant(tenants.user);
    await cleanupTenant(tenants.user2);
  } catch {
    // ignore — cleanup is best-effort
  } finally {
    try {
      unlinkSync(TENANTS_PATH);
    } catch {
      // ignore
    }
  }
}

export default globalTeardown;
