/**
 * check-public-router.ts
 *
 * Guard the unauthenticated surface of the merged flow-api OpenAPI
 * spec. After R6 Phase 0 folded time-api into flow-api, the public
 * (auth-free) surface lives in apps/flow-api/internal/http/router/
 * router.go::buildPublicShareAPI, which is mounted under chi groups
 * that deliberately skip RequireAuth.
 *
 * This check exists because the spec at packages/sdk/openapi.json does
 * not yet emit OpenAPI `security` keys (no securityScheme is registered
 * in Huma), so we cannot introspect each operation's auth requirement
 * directly. The next best thing is to enforce a path-prefix allowlist
 * and fail when:
 *
 *   1. an expected public path is missing from the spec (regression
 *      against ADR 0007); or
 *   2. an unexpected path appears under a public prefix (drift — a new
 *      route was registered in the public chi.Group when it should
 *      have been authenticated).
 *
 * Usage:
 *
 *   bun run scripts/check-public-router.ts
 *
 * Exit codes:
 *   0 — public surface matches the allowlist
 *   1 — drift detected, see stderr for the offending paths
 */

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '..');
const specPath = resolve(repoRoot, 'packages/sdk/openapi.json');

interface OpenApiSpec {
  paths?: Record<string, Record<string, unknown>>;
}

const raw = readFileSync(specPath, 'utf8');
const spec = JSON.parse(raw) as OpenApiSpec;
const allPaths = Object.keys(spec.paths ?? {}).sort();

// Allowlist of path *patterns* that are intentionally auth-free in the
// merged router. Each entry is either an exact path or a prefix marked
// with a trailing slash; a path matches if it equals the entry or starts
// with the prefix.
//
// Keep this list aligned with apps/flow-api/internal/http/router/
// router.go::buildPublicShareAPI. Adding a new public route requires
// updating both. Never widen this list to "fix" a CI failure — the
// failure is the signal that a route should be authenticated.
const publicPatterns: ReadonlyArray<string> = [
  '/health',
  '/share/cal/{token}',
  '/public/lenses/{token}',
  '/public/invites/accept',
  '/invites/{token}/info',
  '/invites/{token}/accept',
];

// publicPrefixes is the prefix-set we use for the "no auth-required
// path leaks into a public namespace" half of the check. Any path
// starting with one of these prefixes MUST appear in publicPatterns;
// otherwise an authed route was accidentally mounted in the public
// chi.Group.
const publicPrefixes: ReadonlyArray<string> = ['/share/cal/', '/public/', '/invites/'];

function matchesPattern(path: string, pattern: string): boolean {
  if (pattern.endsWith('/')) {
    return path.startsWith(pattern);
  }
  return path === pattern;
}

const missingPublic: string[] = [];
for (const pattern of publicPatterns) {
  const present = allPaths.some((p) => matchesPattern(p, pattern));
  if (!present) {
    missingPublic.push(pattern);
  }
}

const leakedAuthed: string[] = [];
for (const p of allPaths) {
  const inPublicNamespace = publicPrefixes.some((pref) => p.startsWith(pref));
  if (!inPublicNamespace) continue;
  const isAllowlisted = publicPatterns.some((pattern) => matchesPattern(p, pattern));
  if (!isAllowlisted) {
    leakedAuthed.push(p);
  }
}

let failed = false;

if (missingPublic.length > 0) {
  failed = true;
  console.error('check-public-router: missing expected public path(s):');
  for (const p of missingPublic) console.error(`  - ${p}`);
}

if (leakedAuthed.length > 0) {
  failed = true;
  console.error(
    'check-public-router: path(s) inside a public namespace are not on the allowlist; either move the route out of the public chi.Group or extend publicPatterns deliberately:',
  );
  for (const p of leakedAuthed) console.error(`  - ${p}`);
}

if (failed) {
  process.exit(1);
}

console.info(
  `check-public-router: ${publicPatterns.length} allowlisted public patterns matched, ${allPaths.length} total paths checked`,
);
