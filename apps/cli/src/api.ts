// API client factory for the tnk CLI.
// Uses @nodate-flow/sdk with stored credentials.

import { type NodateFlowClient, createClient } from '@nodate-flow/sdk';

import { getAuthApiUrl, getFlowApiUrl, loadCredentials } from './config.js';

/**
 * Creates a typed SDK client for the flow API, authenticated with
 * the stored access token. Throws when no credentials are found.
 */
export function createFlowClient(): NodateFlowClient {
  const creds = loadCredentials();
  if (!creds) {
    throw new Error('Not logged in. Run `tnk auth login` to authenticate first.');
  }
  return createClient({
    baseUrl: getFlowApiUrl(),
    token: creds.accessToken,
  });
}

/**
 * Creates a typed SDK client for the auth API (unauthenticated).
 * Used for login / register flows before credentials exist.
 */
export function createAuthClient(): NodateFlowClient {
  return createClient({
    baseUrl: getAuthApiUrl(),
  });
}
