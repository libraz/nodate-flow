/**
 * The only module in flow-web that holds an HTTP client.
 *
 * Everything under `features/` and `routes/` reaches the network
 * through the functions exported here, and a lint rule stops those
 * folders from importing `lib/sdk` directly. The rule is the point:
 * the previous arrangement had one shared converter and three
 * feature-local rewrites of it under a different name, each dropping
 * the HTTP status, the translation key, the RFC 9457 extensions and
 * the recovery hint. Nothing detected them, because a copy under a new
 * name is not a missing call — it looks like ordinary feature code.
 *
 * A door that can only be walked through in one place is checkable; a
 * convention that every call site must remember is not.
 */

import { createApiRequester } from '@nodate-flow/sdk';

import { authSdk, sdk } from './sdk';

const flowApi = createApiRequester(sdk);
const authApi = createApiRequester(authSdk);

/** Calls flow-api, converting any failure into an `ApiError`. */
export const apiRequest = flowApi.request;
/** Calls flow-api and hands back the response alongside the body. */
export const apiRequestDetailed = flowApi.requestDetailed;
/** Reports the HTTP status of a flow-api call without treating a non-2xx as a failure. */
export const apiProbe = flowApi.probe;
/**
 * Runs a flow-api call and hands back the outcome instead of throwing,
 * for the few sites that must read the raw response of a refusal — a
 * `Retry-After` header, say, which no error envelope carries.
 */
export const apiSettle = flowApi.settle;

/** Calls auth-api (identity, workspaces, invites), converting any failure into an `ApiError`. */
export const authApiRequest = authApi.request;
/** Calls auth-api and hands back the response alongside the body. */
export const authApiRequestDetailed = authApi.requestDetailed;
/** Reports the HTTP status of an auth-api call without treating a non-2xx as a failure. */
export const authApiProbe = authApi.probe;
/** As {@link apiSettle}, against auth-api. */
export const authApiSettle = authApi.settle;
