/**
 * Global teardown for flow-web E2E tests.
 *
 * Logs out the shared tenants created by global-setup. The temp JSON
 * file is intentionally left on disk so that:
 *
 *   1. Spec runs that retry after a worker crash can still re-read it
 *      (workers run in separate processes, so the in-memory cache in
 *      load-tenants.ts is per-worker — disk is the only shared store).
 *
 *   2. A residual file from a previous run is harmless: global-setup
 *      always writes a new pair of tenants on every Playwright
 *      invocation (each tenant's email is a fresh randomUUID), so the
 *      next run overwrites whatever is there.
 *
 *   3. Deleting the file mid-run was the root cause of intermittent
 *      ENOENT in load-tenants.ts: when an earlier test failed
 *      catastrophically (page-load timeout, worker restart) the runner
 *      could re-enter teardown unexpectedly and wipe the file before
 *      the remaining specs in the same run had a chance to read it.
 *      Keeping the file pinned through the whole run eliminates that
 *      class of failure entirely.
 *
 * Cleanup is still best-effort — failures are swallowed because we
 * never want teardown to mask a real test failure.
 */

import { existsSync, readFileSync } from 'node:fs';

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
  }
  // Intentionally do NOT unlink TENANTS_PATH. See module docstring above.
}

export default globalTeardown;
