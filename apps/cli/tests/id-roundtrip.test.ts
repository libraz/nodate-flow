/**
 * The ids `tnk` prints must be the ids `tnk` accepts.
 *
 * Every task path resolves its `{id}` by parsing a UUID, so an id that
 * a list command abbreviates for display can never be pasted into
 * `task view` or `task update` — the server answers "Task not found".
 * These tests take the id straight out of the rendered table and feed
 * it back into the commands that consume one.
 */

import { Writable } from 'node:stream';

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { createFlowClient, createIdentityClient } from '../src/api.js';
import { cli } from '../src/index.js';

const TASK_ID = '4f7a1c02-5e3b-7a19-b8d4-2c61f0e9a377';
const TASK_TITLE = 'Ship the release';
const PROJECT_ID = '9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab';
const WORKSPACE_ID = '0190f3a6-4e6c-7d2a-94c9-aa86b1f72c11';

const task = {
  id: TASK_ID,
  projectId: PROJECT_ID,
  title: TASK_TITLE,
  derivedState: 'in_progress',
  priority: 2,
  visibility: 'public',
  dueOn: '2026-06-15',
  projectIdentifier: 'PRJ',
  taskNumber: 42,
};

const get = vi.fn();
const patch = vi.fn();
const post = vi.fn();

vi.mock('../src/api.js', () => {
  const client = {
    GET: (...args: unknown[]) => get(...args),
    PATCH: (...args: unknown[]) => patch(...args),
    POST: (...args: unknown[]) => post(...args),
  };
  return {
    createAuthClient: vi.fn(() => client),
    createFlowClient: vi.fn(() => client),
    createIdentityClient: vi.fn(() => client),
    extractRefreshTokenFromSetCookie: vi.fn(() => undefined),
  };
});

/** Runs a command and returns everything it wrote to stdout. */
async function run(input: string): Promise<string> {
  let captured = '';
  const stdout = new Writable({
    write(chunk, _encoding, callback) {
      captured += String(chunk);
      callback();
    },
  });
  const stderr = new Writable({
    write(_chunk, _encoding, callback) {
      callback();
    },
  });
  await cli.exec(input, { stdout, stderr });
  return captured;
}

/**
 * Pulls the first column out of the table row describing `title`. The
 * table pads columns with spaces, so the id is the first non-empty
 * token on the line.
 */
function printedIdFor(output: string, title: string): string {
  const row = output.split('\n').find((line) => line.includes(title));
  expect(row, `no table row rendered for "${title}"`).toBeDefined();
  const [id] = (row ?? '').trim().split(' ').filter(Boolean);
  return id ?? '';
}

describe('printed task ids round-trip', () => {
  beforeEach(() => {
    get.mockReset();
    patch.mockReset();
    post.mockReset();
    vi.mocked(createFlowClient).mockClear();
    vi.mocked(createIdentityClient).mockClear();
    get.mockImplementation(async (path: string) => {
      if (path === '/tasks') return { data: { tasks: [task], total: 1, nextCursor: null } };
      return { data: task };
    });
    patch.mockResolvedValue({ data: task });
  });

  it('prints an id `task view` resolves', async () => {
    const listed = printedIdFor(await run(`task list --workspace-id ${WORKSPACE_ID}`), TASK_TITLE);
    expect(listed).toBe(TASK_ID);

    await run(`task view ${listed}`);

    expect(get).toHaveBeenLastCalledWith('/tasks/{id}', {
      params: { path: { id: TASK_ID } },
    });
  });

  it('prints an id `task update` resolves', async () => {
    const listed = printedIdFor(await run(`task list --workspace-id ${WORKSPACE_ID}`), TASK_TITLE);

    await run(`task update ${listed} --title "Renamed"`);

    expect(patch).toHaveBeenCalledWith('/tasks/{id}', {
      params: { path: { id: TASK_ID } },
      body: { title: 'Renamed' },
    });
  });

  it('prints an id `task view` resolves after `task search`', async () => {
    const listed = printedIdFor(
      await run(`task search "release" --workspace-id ${WORKSPACE_ID}`),
      TASK_TITLE,
    );
    expect(listed).toBe(TASK_ID);

    await run(`task view ${listed}`);

    expect(get).toHaveBeenLastCalledWith('/tasks/{id}', {
      params: { path: { id: TASK_ID } },
    });
  });
});
