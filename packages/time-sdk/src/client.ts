import createOpenapiFetch, { type Client, type ClientOptions } from 'openapi-fetch';

import type { paths } from './openapi';

export interface CreateClientOptions {
  /** Base URL of the nodate-time HTTP API, e.g. https://api.example.com. */
  baseUrl: string;
  /** Optional static bearer access token. Prefer {@link tokenProvider} when the token can rotate. */
  token?: string;
  /** Optional per-request token provider. Takes precedence over {@link token}. */
  tokenProvider?: () => string | undefined;
  /** Extra fetch options forwarded to openapi-fetch. */
  fetchOptions?: Omit<ClientOptions, 'baseUrl' | 'headers'>;
}

/**
 * createClient returns a typed openapi-fetch client bound to the
 * generated nodate-time OpenAPI schema.
 */
export function createClient(options: CreateClientOptions): Client<paths> {
  const client = createOpenapiFetch<paths>({
    baseUrl: options.baseUrl,
    credentials: 'include',
    ...options.fetchOptions,
  });
  client.use({
    onRequest({ request }) {
      const t = options.tokenProvider?.() ?? options.token;
      if (t) request.headers.set('Authorization', `Bearer ${t}`);
      return request;
    },
  });
  return client;
}

export type NodateTimeClient = Client<paths>;
