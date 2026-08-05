/**
 * Service-routing tests for the `tnk` commands.
 *
 * The builder tests inject a fake SDK client directly, so they cannot
 * see which service a command talks to. nodate-flow splits its HTTP
 * surface across two binaries — the auth API owns identity and
 * workspaces, the flow API owns tasks, projects and calendar — and
 * picking the wrong client turns every response into a 404. These tests
 * run the real command actions with the client factories mocked and
 * assert which factory each command reaches for.
 */

import { Writable } from 'node:stream';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createFlowClient, createIdentityClient } from '../src/api.js';
import { cli } from '../src/index.js';

const emptyPage = { data: { workspaces: [], projects: [], tasks: [], total: 0 } };

function createClientStub() {
  return {
    GET: vi.fn().mockResolvedValue(emptyPage),
  };
}

vi.mock('../src/api.js', () => ({
  createAuthClient: vi.fn(() => createClientStub()),
  createFlowClient: vi.fn(() => createClientStub()),
  createIdentityClient: vi.fn(() => createClientStub()),
  extractRefreshTokenFromSetCookie: vi.fn(() => undefined),
}));

/** Collects everything a command writes so it never reaches the console. */
function sink(): Writable {
  return new Writable({
    write(_chunk, _encoding, callback) {
      callback();
    },
  });
}

async function run(input: string): Promise<void> {
  await cli.exec(input, { stdout: sink(), stderr: sink() });
}

describe('command service routing', () => {
  beforeEach(() => {
    vi.mocked(createFlowClient).mockClear();
    vi.mocked(createIdentityClient).mockClear();
  });

  afterEach(() => {
    process.exitCode = 0;
  });

  it('routes `workspace list` to the auth API', async () => {
    await run('workspace list');

    expect(createIdentityClient).toHaveBeenCalledTimes(1);
    expect(createFlowClient).not.toHaveBeenCalled();
  });

  it('routes `project list` to the flow API', async () => {
    await run('project list --workspace-id 0190f3a6-4e6c-7d2a-94c9-aa86b1f72c11');

    expect(createFlowClient).toHaveBeenCalledTimes(1);
    expect(createIdentityClient).not.toHaveBeenCalled();
  });

  it('routes `task list` to the flow API', async () => {
    await run('task list');

    expect(createFlowClient).toHaveBeenCalledTimes(1);
    expect(createIdentityClient).not.toHaveBeenCalled();
  });
});
