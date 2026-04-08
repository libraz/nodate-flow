// Thin typed client factory wrapping openapi-fetch with the generated
// nodate-flow OpenAPI paths. Consumers call createClient once with a
// base URL and an optional bearer token and get a fully typed fetch
// client back.

import createOpenapiFetch, {
  type Client,
  type ClientOptions,
} from 'openapi-fetch';

import type { paths } from './openapi';

/** Options accepted by createClient. */
export interface CreateClientOptions {
  /** Base URL of the nodate-flow HTTP API, e.g. https://api.example.com. */
  baseUrl: string;
  /** Optional bearer access token; sent as `Authorization: Bearer <token>`. */
  token?: string;
  /** Extra fetch options forwarded to openapi-fetch. */
  fetchOptions?: Omit<ClientOptions, 'baseUrl' | 'headers'>;
}

/**
 * createClient returns a typed openapi-fetch client bound to the
 * generated nodate-flow OpenAPI schema.
 */
export function createClient(
  options: CreateClientOptions
): Client<paths> {
  const headers: Record<string, string> = {};
  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  return createOpenapiFetch<paths>({
    baseUrl: options.baseUrl,
    headers,
    ...options.fetchOptions,
  });
}

export type NodateFlowClient = Client<paths>;
