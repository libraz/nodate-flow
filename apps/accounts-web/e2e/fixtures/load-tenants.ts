/**
 * Loads the shared tenants created by global-setup.
 */

import { readFileSync } from 'node:fs';

import { type SharedTenants, TENANTS_PATH } from './global-setup';

let cached: SharedTenants | null = null;

export function loadTenants(): SharedTenants {
  if (cached) return cached;
  cached = JSON.parse(readFileSync(TENANTS_PATH, 'utf-8')) as SharedTenants;
  return cached;
}
