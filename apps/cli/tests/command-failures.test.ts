/**
 * Failure handling of the `tnk` commands, run through the real actions
 * with the client factories and the credential file mocked.
 */

import { Writable } from 'node:stream';

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { revokeSession } from '../src/api.js';
import { clearCredentials, loadCredentials } from '../src/config.js';
import { cli } from '../src/index.js';
import { EXIT_AUTH, EXIT_RUNTIME } from '../src/util/exit.js';

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
    revokeSession: vi.fn(async () => true),
  };
});

vi.mock('../src/config.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/config.js')>();
  return { ...actual, loadCredentials: vi.fn(), clearCredentials: vi.fn() };
});

const TASK_ID = '4f7a1c02-5e3b-7a19-b8d4-2c61f0e9a377';
const PROJECT_ID = '9c2ad1d8-1f2c-7e1c-9a8a-44c0c9f0c1ab';
const WORKSPACE_ID = '0190f3a6-4e6c-7d2a-94c9-aa86b1f72c11';

/** A gateway failure: non-2xx and no body for openapi-fetch to parse. */
function bodylessFailure(status = 502) {
  return { data: undefined, error: undefined, response: new Response(null, { status }) };
}

async function run(input: string): Promise<{ stdout: string; stderr: string }> {
  const out = { stdout: '', stderr: '' };
  const sink = (key: 'stdout' | 'stderr') =>
    new Writable({
      write(chunk, _encoding, callback) {
        out[key] += String(chunk);
        callback();
      },
    });
  await cli.exec(input, { stdout: sink('stdout'), stderr: sink('stderr') });
  return out;
}

beforeEach(() => {
  get.mockReset();
  patch.mockReset();
  post.mockReset();
  vi.mocked(loadCredentials).mockReset();
  vi.mocked(clearCredentials).mockReset();
  vi.mocked(revokeSession).mockReset().mockResolvedValue(true);
});

afterEach(() => {
  process.exitCode = 0;
});

describe('a non-2xx response with no body', () => {
  it.each([
    ['workspace list', 'No workspaces found'],
    [`project list --workspace-id ${WORKSPACE_ID}`, 'No projects found'],
    [`task list --workspace-id ${WORKSPACE_ID}`, 'No tasks found'],
    [`task search ship --workspace-id ${WORKSPACE_ID}`, 'No tasks found'],
  ])('fails `%s` instead of reporting an empty result', async (command, emptyMessage) => {
    get.mockResolvedValue(bodylessFailure());

    const { stdout, stderr } = await run(command);

    expect(process.exitCode).toBe(EXIT_RUNTIME);
    expect(stdout).not.toContain(emptyMessage);
    expect(stderr).toContain('Error');
  });

  it('fails `task create`', async () => {
    post.mockResolvedValue(bodylessFailure());

    const { stdout, stderr } = await run(`task create --title Ship --project-id ${PROJECT_ID}`);

    expect(process.exitCode).toBe(EXIT_RUNTIME);
    expect(stdout).not.toContain('Created');
    expect(stderr).toContain('Failed to create task');
  });

  it('fails `task update` without issuing the follow-up transition', async () => {
    patch.mockResolvedValue(bodylessFailure());

    const { stdout, stderr } = await run(`task update ${TASK_ID} --title New --state start`);

    expect(process.exitCode).toBe(EXIT_RUNTIME);
    expect(stdout).not.toContain('Updated');
    expect(stderr).toContain('Failed to update task');
    expect(post).not.toHaveBeenCalled();
  });

  it('fails `task view`', async () => {
    get.mockResolvedValue(bodylessFailure(404));

    const { stdout } = await run(`task view ${TASK_ID}`);

    expect(process.exitCode).toBe(EXIT_RUNTIME);
    expect(stdout).toBe('');
  });
});

describe('a 401 that survives the refresh', () => {
  it('exits with the authentication code', async () => {
    get.mockResolvedValue({
      error: { type: 'AUTH.SESSION.UNAUTHORIZED', title: 'Unauthorized', status: 401 },
      response: new Response(null, { status: 401 }),
    });

    await run(`task list --workspace-id ${WORKSPACE_ID}`);

    expect(process.exitCode).toBe(EXIT_AUTH);
  });

  it('exits with the authentication code when the 401 has no body', async () => {
    get.mockResolvedValue(bodylessFailure(401));

    await run('workspace list');

    expect(process.exitCode).toBe(EXIT_AUTH);
  });
});

describe('field-level validation errors', () => {
  it('names the refused field and the reason', async () => {
    post.mockResolvedValue({
      error: {
        type: 'WS.VALIDATION.FAILED',
        title: 'Unprocessable Entity',
        status: 422,
        errors: [{ location: 'body.title', message: 'expected length <= 255' }],
      },
      response: new Response(null, { status: 422 }),
    });

    const { stderr } = await run(`task create --title Ship --project-id ${PROJECT_ID}`);

    expect(stderr).toContain('body.title: expected length <= 255');
  });
});

describe('tnk auth logout', () => {
  const creds = {
    accessToken: 'access',
    refreshToken: 'refresh',
    apiBaseUrl: 'https://flow.test',
  };

  it('revokes the server session before removing the file', async () => {
    vi.mocked(loadCredentials).mockReturnValue(creds);

    const { stdout, stderr } = await run('auth logout');

    expect(revokeSession).toHaveBeenCalledWith(creds);
    expect(clearCredentials).toHaveBeenCalledTimes(1);
    expect(stdout).toContain('Logged out');
    expect(stderr).toBe('');
  });

  it('warns and still removes the file when revocation cannot be confirmed', async () => {
    vi.mocked(loadCredentials).mockReturnValue(creds);
    vi.mocked(revokeSession).mockResolvedValue(false);

    const { stdout, stderr } = await run('auth logout');

    expect(clearCredentials).toHaveBeenCalledTimes(1);
    expect(stderr).toContain('Warning');
    expect(stdout).not.toContain('Logged out');
  });
});

describe('tnk auth status', () => {
  it('does not claim a validated session from a credentials file alone', async () => {
    vi.mocked(loadCredentials).mockReturnValue({
      accessToken: 'access',
      refreshToken: 'revoked',
      apiBaseUrl: 'https://flow.test',
    });

    const { stdout } = await run('auth status');

    expect(stdout).not.toContain('Authenticated');
    expect(stdout).toContain('Credentials stored');
  });
});
