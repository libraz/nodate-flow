import { spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { testState } = vi.hoisted(() => ({ testState: { home: '' } }));

vi.mock('node:os', async (importOriginal) => {
  const actual = await importOriginal<typeof import('node:os')>();
  return { ...actual, homedir: () => testState.home };
});

const configDir = () => path.join(testState.home, '.config', 'tnk');
const credentialsPath = () => path.join(configDir(), 'credentials.json');

const creds = {
  accessToken: 'access',
  refreshToken: 'refresh',
  apiBaseUrl: 'https://flow.test',
};

describe('credentials store', () => {
  beforeEach(() => {
    testState.home = fs.mkdtempSync(path.join(os.tmpdir(), 'tnk-cli-store-'));
    vi.resetModules();
  });

  afterEach(() => {
    fs.rmSync(testState.home, { recursive: true, force: true });
    testState.home = '';
    vi.resetModules();
  });

  it('tightens a pre-existing world-readable file and directory to owner-only', async () => {
    fs.mkdirSync(configDir(), { recursive: true, mode: 0o755 });
    fs.chmodSync(configDir(), 0o755);
    fs.writeFileSync(credentialsPath(), '{}', { mode: 0o644 });
    fs.chmodSync(credentialsPath(), 0o644);

    const { saveCredentials } = await import('../src/config.js');
    saveCredentials(creds);

    expect(fs.statSync(credentialsPath()).mode & 0o777).toBe(0o600);
    expect(fs.statSync(configDir()).mode & 0o777).toBe(0o700);
    expect(JSON.parse(fs.readFileSync(credentialsPath(), 'utf-8'))).toEqual(creds);
    expect(fs.readdirSync(configDir())).toEqual(['credentials.json']);
  });

  it('never lets a concurrent reader observe a partially written file', async () => {
    const { saveCredentials } = await import('../src/config.js');
    saveCredentials(creds);

    // A separate process reads the file in a tight loop while this one
    // rewrites it with a payload large enough to need several writes.
    const reader = spawn(
      process.execPath,
      [
        '-e',
        `const fs = require('node:fs');
         let bad = 0;
         const end = Date.now() + 1500;
         while (Date.now() < end) {
           try { JSON.parse(fs.readFileSync(process.argv[1], 'utf-8')); }
           catch (e) { if (e.code !== 'ENOENT') bad++; }
         }
         process.stdout.write(String(bad));`,
        credentialsPath(),
      ],
      { stdio: ['ignore', 'pipe', 'inherit'] },
    );
    let output = '';
    reader.stdout.on('data', (chunk) => {
      output += String(chunk);
    });
    const exited = new Promise<void>((resolve) => reader.on('exit', () => resolve()));

    const big = 'x'.repeat(256 * 1024);
    const end = Date.now() + 1200;
    for (let i = 0; Date.now() < end; i++) {
      saveCredentials({ ...creds, accessToken: `${big}${i}` });
      await new Promise((resolve) => setImmediate(resolve));
    }
    await exited;

    expect(output).toBe('0');
  });

  it('serializes holders of the credentials lock', async () => {
    const { withCredentialsLock } = await import('../src/config.js');
    const order: string[] = [];
    let releaseFirst!: () => void;
    const firstHeld = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });

    const first = withCredentialsLock(async () => {
      order.push('first:start');
      await firstHeld;
      order.push('first:end');
    });
    const second = withCredentialsLock(async () => {
      order.push('second:start');
    });

    await new Promise((resolve) => setTimeout(resolve, 200));
    expect(order).toEqual(['first:start']);
    releaseFirst();
    await Promise.all([first, second]);

    expect(order).toEqual(['first:start', 'first:end', 'second:start']);
    expect(fs.existsSync(`${credentialsPath()}.lock`)).toBe(false);
  });
});
