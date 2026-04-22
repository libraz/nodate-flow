/**
 * Global teardown: clean up shared tenants and temp file.
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
    await cleanupTenant(tenants.admin);
  } catch {
    // Best-effort cleanup
  }

  try {
    unlinkSync(TENANTS_PATH);
  } catch {
    // ignore
  }
}

export default globalTeardown;
