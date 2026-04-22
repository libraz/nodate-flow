/**
 * Global setup for accounts-web E2E tests.
 *
 * Creates a shared pool of test tenants ONCE before any spec runs,
 * avoiding rate-limit issues from parallel tenant creation.
 * Tenants are stored in a temp JSON file read by each spec.
 */

import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { createTestTenant, grantInstanceAdmin } from './tenant';

export interface SharedTenants {
  user: Awaited<ReturnType<typeof createTestTenant>>;
  /** Second user tenant for tests that mutate user profile (avoids conflicts). */
  user2: Awaited<ReturnType<typeof createTestTenant>>;
  admin: Awaited<ReturnType<typeof createTestTenant>>;
  /** Whether the admin tenant actually has instance-admin privileges. */
  adminGranted: boolean;
}

const Filename = fileURLToPath(import.meta.url);
const Dirname = dirname(Filename);

export const TENANTS_PATH = join(Dirname, '..', '.tenants.json');

async function globalSetup(): Promise<void> {
  // Create tenants sequentially to avoid rate limiting
  const user = await createTestTenant();

  // Wait before next registration to avoid rate limiter
  await new Promise((r) => setTimeout(r, 2000));

  const user2 = await createTestTenant();

  await new Promise((r) => setTimeout(r, 2000));

  const admin = await createTestTenant();
  const adminGranted = await grantInstanceAdmin(admin);

  const tenants: SharedTenants = { user, user2, admin, adminGranted };
  writeFileSync(TENANTS_PATH, JSON.stringify(tenants, null, 2));
}

export default globalSetup;
