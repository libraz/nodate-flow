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

import { createTestTenant, grantInstanceAdmin, seedWorkspace } from './tenant';

export interface SharedTenants {
  user: Awaited<ReturnType<typeof createTestTenant>>;
  /** Second user tenant for tests that mutate user profile (avoids conflicts). */
  user2: Awaited<ReturnType<typeof createTestTenant>>;
  admin: Awaited<ReturnType<typeof createTestTenant>>;
  /**
   * Public id of the workspace owned by `user`. Seeded so `/workspaces`
   * member-facing tests always have a row to click.
   */
  userWorkspaceId: string;
  /**
   * Public id of the workspace owned by `admin`. Seeded so the
   * admin-facing `/admin/workspaces` list always has a row to click,
   * independent of what other parallel runs create.
   */
  adminWorkspaceId: string;
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
  // Throws if the grant cannot be made. The admin specs have no
  // meaningful degraded mode, so a failure here has to stop the run
  // rather than let five spec files skip themselves into a green report.
  await grantInstanceAdmin(admin);

  // Seed one workspace per visible tenant so list pages
  // (`/workspaces`, `/admin/workspaces`) always have at least one row
  // and downstream tests don't need to skip on empty state.
  const userWorkspaceId = await seedWorkspace(user);
  const adminWorkspaceId = await seedWorkspace(admin);

  const tenants: SharedTenants = {
    user,
    user2,
    admin,
    userWorkspaceId,
    adminWorkspaceId,
  };
  writeFileSync(TENANTS_PATH, JSON.stringify(tenants, null, 2));
}

export default globalSetup;
