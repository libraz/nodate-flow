/**
 * Loads the shared tenants created by global-setup.
 *
 * The file is written once by global-setup before any worker starts
 * and (since the teardown fix) is left on disk for the duration of the
 * run. Each Playwright worker is a separate Node process, so the
 * in-memory `cached` is per-worker; the on-disk JSON is the
 * cross-worker channel.
 *
 * If the file is missing for any reason — e.g. a stale checkout where
 * global-setup never ran, or someone manually removed
 * apps/flow-web/e2e/.tenants.json mid-run — we throw a loud,
 * actionable error instead of bubbling a raw ENOENT. Re-creating
 * tenants here would silently mask orchestration bugs (each test
 * would create its own pair, multiplying API load and hiding the real
 * problem), so we surface it instead.
 */

import { existsSync, readFileSync } from 'node:fs';

import { type SharedTenants, TENANTS_PATH } from './global-setup';

let cached: SharedTenants | null = null;

export function loadTenants(): SharedTenants {
  if (cached) return cached;
  if (!existsSync(TENANTS_PATH)) {
    throw new Error(
      `loadTenants: ${TENANTS_PATH} is missing. This file is created by e2e/fixtures/global-setup.ts before any spec runs and must persist for the entire Playwright invocation. If you see this, either global-setup did not run (run via 'bunx playwright test', not by importing the spec directly) or something deleted the file mid-run (check global-teardown.ts — it must NOT unlink the file).`,
    );
  }
  const raw = readFileSync(TENANTS_PATH, 'utf-8');
  if (!raw.trim()) {
    throw new Error(
      `loadTenants: ${TENANTS_PATH} is empty. global-setup likely crashed mid-write.`,
    );
  }
  cached = JSON.parse(raw) as SharedTenants;
  return cached;
}
