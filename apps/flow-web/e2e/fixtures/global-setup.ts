/**
 * Global setup for flow-web E2E tests.
 *
 * Creates a shared pool of test tenants ONCE before any spec runs,
 * avoiding rate-limit and parallelism issues from concurrent tenant
 * creation. Tenants are stored in a temp JSON file read by each spec.
 */

import { renameSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { createTask, createTestTenant } from './tenant';

/** Pre-seeded task names that individual specs can look for. */
export const SEEDED_TASKS = {
  smoke: 'Seeded smoke task',
  board: 'Seeded board task',
  calendarApril: 'Seeded calendar task April',
  ganttA: 'Seeded gantt task A',
  ganttB: 'Seeded gantt task B',
} as const;

export interface SharedTenants {
  /** General-purpose tenant for most tests. */
  user: Awaited<ReturnType<typeof createTestTenant>>;
  /** Second tenant for tests that need two users. */
  user2: Awaited<ReturnType<typeof createTestTenant>>;
  /** Pre-seeded task names for test assertions. */
  seededTasks: typeof SEEDED_TASKS;
}

const Filename = fileURLToPath(import.meta.url);
const Dirname = dirname(Filename);

export const TENANTS_PATH = join(Dirname, '..', '.tenants.json');

async function globalSetup(): Promise<void> {
  const user = await createTestTenant();
  // Pre-seed tasks for user before creating second tenant
  // (spreads API calls over time to avoid rate limiting).
  await createTask(user, SEEDED_TASKS.smoke);
  await createTask(user, SEEDED_TASKS.board);
  await createTaskWithDueDate(user, SEEDED_TASKS.calendarApril, '2026-04-12');
  await createTaskWithDueDate(user, SEEDED_TASKS.ganttA, '2026-04-15');
  await createTaskWithDueDate(user, SEEDED_TASKS.ganttB, '2026-04-22');

  // Create second tenant after tasks — gives rate limit windows time to reset.
  const user2 = await createTestTenant();

  const tenants: SharedTenants = { user, user2, seededTasks: SEEDED_TASKS };
  // Atomic write: write to a temp file and rename. Prevents readers
  // (loadTenants) from observing a partially-written file if the
  // process is interrupted mid-write.
  const tmpPath = `${TENANTS_PATH}.tmp`;
  writeFileSync(tmpPath, JSON.stringify(tenants, null, 2));
  renameSync(tmpPath, TENANTS_PATH);
}

async function createTaskWithDueDate(
  tenant: Awaited<ReturnType<typeof createTestTenant>>,
  title: string,
  dueOn: string,
): Promise<void> {
  const API_BASE_URL = process.env.NF_API_URL ?? 'http://localhost:8080';
  const res = await fetch(`${API_BASE_URL}/tasks`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      accept: 'application/json',
      authorization: `Bearer ${tenant.accessToken}`,
    },
    body: JSON.stringify({ title, dueOn, projectId: tenant.projectId }),
  });
  if (!res.ok) {
    throw new Error(`POST /tasks -> ${res.status} ${await res.text()}`);
  }
}

export default globalSetup;
