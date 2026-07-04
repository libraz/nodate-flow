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
    if (
      parsed !== null &&
      typeof parsed === 'object' &&
      'accessToken' in parsed &&
      'apiBaseUrl' in parsed
    ) {
      return parsed as Credentials;
    }
    return undefined;
  } catch {
    return undefined;
  }
}

/** Persists credentials to disk, creating the config directory if needed. */
export function saveCredentials(creds: Credentials): void {
  fs.mkdirSync(CONFIG_DIR, { recursive: true });
  fs.writeFileSync(CREDENTIALS_PATH, `${JSON.stringify(creds, null, 2)}\n`, {
    mode: 0o600,
  });
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

/**
 * Returns the flow API base URL. Checks NF_FLOW_API_URL env, then
 * stored credentials apiBaseUrl, then falls back to localhost.
 */
export function getFlowApiUrl(): string {
  const creds = loadCredentials();
  return process.env.NF_FLOW_API_URL ?? creds?.apiBaseUrl ?? DEFAULT_FLOW_API_URL;
}
