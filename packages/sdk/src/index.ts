// Public entry point for the generated nodate-flow SDK.
//
// The generated OpenAPI type module (./openapi) is produced by
// `make gen-sdk` which runs `openapi-typescript` against
// packages/sdk/openapi.json. The error-code modules under ./errors are
// produced by the errors codegen (scripts/gen-errors).

export type { paths, components, operations } from './openapi';
export { createClient, type CreateClientOptions, type NodateFlowClient } from './client';
export * from './errors';
