import { type NodateFlowClient, createClient } from '@nodate-flow/sdk';

const baseUrl =
  (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://localhost:8080';

/**
 * Singleton SDK client for the web app. The token provider returns
 * undefined during F0; it will be wired to the auth slice in F3.
 */
export const sdk: NodateFlowClient = createClient({
  baseUrl,
  tokenProvider: () => undefined,
});
