/**
 * The only module in accounts-web that holds an HTTP client.
 *
 * Everything under `features/` and `routes/` reaches the network
 * through the functions exported here, and a lint rule stops those
 * folders from importing `lib/sdk` directly. Screens in this app act
 * on credentials, so a failure that arrives without being recognised
 * as one is not a cosmetic problem: a two-factor toggle that reports
 * success against a server that never applied it invites its owner to
 * delete the authenticator app they still need.
 */

import { createApiRequester } from '@nodate-flow/sdk';

import { sdk } from './sdk';

const authApi = createApiRequester(sdk);

/** Calls auth-api, converting any failure into an `ApiError`. */
export const apiRequest = authApi.request;
/** Calls auth-api and hands back the response alongside the body. */
export const apiRequestDetailed = authApi.requestDetailed;
/** Reports the HTTP status of a call without treating a non-2xx as a failure. */
export const apiProbe = authApi.probe;

/**
 * Runs a call and hands back the outcome instead of throwing, for the
 * few sites that must read the raw response of a refusal — the sign-in
 * cooldown rides in a `Retry-After` header, which no error envelope
 * carries.
 */
export const apiSettle = authApi.settle;
