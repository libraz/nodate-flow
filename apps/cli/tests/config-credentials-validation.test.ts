import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { testState } = vi.hoisted(() => ({ testState: { home: '' } }));

vi.mock('node:os', async (importOriginal) => {
  const actual = await importOriginal<typeof import('node:os')>();
  return { ...actual, homedir: () => testState.home };
});

const credentialsPath = () => path.join(testState.home, '.config', 'tnk', 'credentials.json');

function writeCredentials(contents: string): void {
  const filePath = credentialsPath();
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(filePath, contents, 'utf-8');
}

describe('loadCredentials validation', () => {
  beforeEach(() => {
    testState.home = fs.mkdtempSync(path.join(os.tmpdir(), 'tnk-cli-credentials-'));
    vi.resetModules();
  });

  afterEach(() => {
    fs.rmSync(testState.home, { recursive: true, force: true });
    testState.home = '';
    vi.resetModules();
  });

  it.each([
    [
      'an array',
      '[{"accessToken":"access","refreshToken":"refresh","apiBaseUrl":"https://flow.test"}]',
    ],
    ['a missing access token', '{"refreshToken":"refresh","apiBaseUrl":"https://flow.test"}'],
    [
      'an empty access token',
      '{"accessToken":"","refreshToken":"refresh","apiBaseUrl":"https://flow.test"}',
    ],
    [
      'a non-string access token',
      '{"accessToken":1,"refreshToken":"refresh","apiBaseUrl":"https://flow.test"}',
    ],
    ['a missing API base URL', '{"accessToken":"access","refreshToken":"refresh"}'],
    ['an empty API base URL', '{"accessToken":"access","refreshToken":"refresh","apiBaseUrl":""}'],
    [
      'a non-string API base URL',
      '{"accessToken":"access","refreshToken":"refresh","apiBaseUrl":1}',
    ],
    ['a missing refresh token', '{"accessToken":"access","apiBaseUrl":"https://flow.test"}'],
    [
      'a non-string refresh token',
      '{"accessToken":"access","refreshToken":1,"apiBaseUrl":"https://flow.test"}',
    ],
    [
      'an empty auth API base URL',
      '{"accessToken":"access","refreshToken":"refresh","apiBaseUrl":"https://flow.test","authApiBaseUrl":""}',
    ],
    [
      'a non-string auth API base URL',
      '{"accessToken":"access","refreshToken":"refresh","apiBaseUrl":"https://flow.test","authApiBaseUrl":1}',
    ],
  ])('rejects credentials with %s', async (_description, contents) => {
    writeCredentials(contents);
    const { loadCredentials } = await import('../src/config.js');

    expect(loadCredentials()).toBeUndefined();
  });

  it('loads current credentials with authApiBaseUrl', async () => {
    const credentials = {
      accessToken: 'access',
      refreshToken: 'refresh',
      apiBaseUrl: 'https://flow.test',
      authApiBaseUrl: 'https://auth.test',
    };
    writeCredentials(JSON.stringify(credentials));
    const { loadCredentials } = await import('../src/config.js');

    expect(loadCredentials()).toEqual(credentials);
  });

  it('loads legacy credentials without authApiBaseUrl or with an empty refresh token', async () => {
    const credentials = {
      accessToken: 'access',
      refreshToken: '',
      apiBaseUrl: 'https://flow.test',
    };
    writeCredentials(JSON.stringify(credentials));
    const { loadCredentials } = await import('../src/config.js');

    expect(loadCredentials()).toEqual(credentials);
  });

  it('returns undefined when the credentials file is missing or invalid JSON', async () => {
    const { loadCredentials } = await import('../src/config.js');
    expect(loadCredentials()).toBeUndefined();

    writeCredentials('{invalid');
    expect(loadCredentials()).toBeUndefined();
  });
});
