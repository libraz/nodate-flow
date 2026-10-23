// Credential and configuration storage for the tnk CLI.
// Reads and writes ~/.config/tnk/credentials.json.

import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

/** Stored credentials shape. */
export interface Credentials {
  accessToken: string;
  refreshToken: string;
  apiBaseUrl: string;
  authApiBaseUrl?: string;
}

const CONFIG_DIR = path.join(os.homedir(), '.config', 'tnk');
const CREDENTIALS_PATH = path.join(CONFIG_DIR, 'credentials.json');

/** Default API base URLs when none is configured. */
const DEFAULT_AUTH_API_URL = 'http://localhost:8082';
const DEFAULT_FLOW_API_URL = 'http://localhost:8080';

/**
 * Reads stored credentials from disk.
 * Returns undefined when no credentials file exists.
 */
export function loadCredentials(): Credentials | undefined {
  try {
    const raw = fs.readFileSync(CREDENTIALS_PATH, 'utf-8');
    const parsed: unknown = JSON.parse(raw);
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return undefined;

    const values = parsed as Record<string, unknown>;
    if (
      typeof values.accessToken !== 'string' ||
      values.accessToken.length === 0 ||
      typeof values.refreshToken !== 'string' ||
      typeof values.apiBaseUrl !== 'string' ||
      values.apiBaseUrl.length === 0 ||
      (values.authApiBaseUrl !== undefined &&
        (typeof values.authApiBaseUrl !== 'string' || values.authApiBaseUrl.length === 0))
    ) {
      return undefined;
    }
    return values as unknown as Credentials;
  } catch {
    return undefined;
  }
}

/**
 * Persists credentials to disk. The file is written to a private temp
 * file and renamed over the old one, so a concurrent reader sees either
 * the previous or the new credentials, never a partial file, and the
 * tokens never exist at a mode looser than owner-only. The directory and
 * file modes are reapplied on every save, since `mode` only applies when
 * a path is created.
 */
export function saveCredentials(creds: Credentials): void {
  fs.mkdirSync(CONFIG_DIR, { recursive: true, mode: 0o700 });
  fs.chmodSync(CONFIG_DIR, 0o700);
  const tmpPath = `${CREDENTIALS_PATH}.${process.pid}.${Date.now()}.tmp`;
  try {
    fs.writeFileSync(tmpPath, `${JSON.stringify(creds, null, 2)}\n`, { mode: 0o600, flag: 'wx' });
    fs.chmodSync(tmpPath, 0o600);
    fs.renameSync(tmpPath, CREDENTIALS_PATH);
  } catch (err) {
    fs.rmSync(tmpPath, { force: true });
    throw err;
  }
}

/** How old a credentials lock may grow before it is presumed abandoned. */
const LOCK_STALE_MS = 30_000;

/** Delay between attempts to take a held credentials lock. */
const LOCK_RETRY_MS = 50;

/**
 * Runs `fn` while holding an exclusive lock on the credentials file,
 * shared by every `tnk` process on the machine. Token refresh holds it
 * across reading, refreshing and saving, so two processes can never both
 * present the same refresh token. A lock left behind by a crashed
 * process is broken once it is {@link LOCK_STALE_MS} old.
 */
export async function withCredentialsLock<T>(fn: () => Promise<T>): Promise<T> {
  const lockPath = `${CREDENTIALS_PATH}.lock`;
  fs.mkdirSync(CONFIG_DIR, { recursive: true, mode: 0o700 });
  for (;;) {
    try {
      fs.closeSync(fs.openSync(lockPath, 'wx', 0o600));
      break;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'EEXIST') throw err;
    }
    try {
      if (Date.now() - fs.statSync(lockPath).mtimeMs > LOCK_STALE_MS) {
        fs.rmSync(lockPath, { force: true });
        continue;
      }
    } catch {
      // Released between the attempt and the stat: try again at once.
      continue;
    }
    await new Promise((resolve) => setTimeout(resolve, LOCK_RETRY_MS));
  }
  try {
    return await fn();
  } finally {
    fs.rmSync(lockPath, { force: true });
  }
}

/** Removes the stored credentials file. */
export function clearCredentials(): void {
  try {
    fs.unlinkSync(CREDENTIALS_PATH);
  } catch {
    // Ignore if file does not exist.
  }
}

/**
 * Returns the auth API base URL. Checks NF_AUTH_API_URL env, then
 * falls back to localhost.
 */
export function getAuthApiUrl(): string {
  return process.env.NF_AUTH_API_URL ?? DEFAULT_AUTH_API_URL;
}

/** Returns the auth API URL from env, credentials, then the local default. */
export function resolveAuthApiUrl(creds: Pick<Credentials, 'authApiBaseUrl'> | undefined): string {
  return process.env.NF_AUTH_API_URL ?? creds?.authApiBaseUrl ?? DEFAULT_AUTH_API_URL;
}

/**
 * Returns the flow API base URL. Checks NF_FLOW_API_URL env, then
 * stored credentials apiBaseUrl, then falls back to localhost.
 */
export function getFlowApiUrl(): string {
  const creds = loadCredentials();
  return process.env.NF_FLOW_API_URL ?? creds?.apiBaseUrl ?? DEFAULT_FLOW_API_URL;
}
