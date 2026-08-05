/**
 * check-public-router.ts
 *
 * Guard the unauthenticated surface of flow-api.
 *
 * The public (auth-free) surface lives in
 * apps/flow-api/internal/http/router/router.go::buildPublicShareAPI,
 * mounted under chi groups that deliberately skip RequireAuth. Anything
 * registered there answers without a session, so the set of routes in
 * that builder is a security boundary and has to be enumerated
 * deliberately rather than discovered later.
 *
 * The check has two halves, and they fail for different reasons:
 *
 *   1. Every allowlisted public path is present in the merged spec.
 *      Losing one is a regression against ADR 0007 — a share link that
 *      silently stops resolving.
 *
 *   2. Every route the public builder registers is on the allowlist.
 *      This half reads the Go source rather than the spec, because the
 *      spec cannot say which builder a path came from. Deriving the
 *      public set from path prefixes instead — the previous approach —
 *      only ever inspected paths that already looked public, so a route
 *      registered in the public group under, say, `/workspaces/...`
 *      was invisible to it: exactly the shape of an accidental
 *      exposure, and the one case the check most needs to catch.
 *
 * Registration forms the source scan understands:
 *
 *   - `huma.Register(subAPI, huma.Operation{... Path: "..."}, handler)`
 *     written inline in the builder.
 *   - `pkg.RegisterPublic(subAPI, deps)` delegations, resolved one level
 *     into `internal/http/handlers/<pkg>/` and scanned the same way.
 *   - raw chi verbs (`r.Post("/webhooks/github", ...)`), which never
 *     appear in the OpenAPI document at all and would otherwise be
 *     wholly unguarded.
 *
 * Anything else that looks like a registration fails the check rather
 * than being skipped. A form this script does not understand is a form
 * it cannot vouch for, and silently ignoring it is how a guard turns
 * into decoration.
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
const routerPath = resolve(repoRoot, 'apps/flow-api/internal/http/router/router.go');
const handlersRoot = resolve(repoRoot, 'apps/flow-api/internal/http/handlers');

interface OpenApiSpec {
  paths?: Record<string, Record<string, unknown>>;
}

/**
 * Public paths owned by flow-api's `buildPublicShareAPI`.
 *
 * Verified in both directions against the Go source: the builder may
 * register nothing outside this list, and every entry must still be
 * registered. Adding a public route means updating both. Never widen
 * this list to "fix" a failure — the failure is the signal that a route
 * should be authenticated.
 */
const flowApiPublicPaths: ReadonlyArray<string> = [
  '/health',
  '/share/cal/{token}',
  '/public/lenses/{token}',
  '/public/invites/accept',
];

/**
 * Public paths that reach the merged spec from another service —
 * currently auth-api's workspace-invite routes, which the invite page
 * calls before the visitor has a session.
 *
 * Only their presence in the spec is checked. auth-api's router builds
 * its groups inline rather than through a named public builder, so
 * there is no equivalent function to scan; source-verifying that surface
 * needs a separate pass and is not silently claimed here.
 */
const externalPublicPaths: ReadonlyArray<string> = [
  '/invites/{token}/info',
  '/invites/{token}/accept',
];

/** Everything expected to answer without a session, from any service. */
const publicPatterns: ReadonlyArray<string> = [...flowApiPublicPaths, ...externalPublicPaths];

/**
 * Raw chi routes in the public group. These verify their own signing
 * secrets inside the handler and are not Huma operations, so they are
 * absent from the OpenAPI document and are checked against the source
 * only.
 */
const publicRawRoutes: ReadonlyArray<string> = [
  '/webhooks/github',
  '/webhooks/slack',
  '/webhooks/google',
];

/**
 * Path prefixes that read as public to a human. A path here that is not
 * allowlisted is either an exposure or a misleading name; both want a
 * decision. Complements the source scan rather than replacing it.
 */
const publicPrefixes: ReadonlyArray<string> = ['/share/cal/', '/public/', '/invites/'];

function matchesPattern(path: string, pattern: string): boolean {
  if (pattern.endsWith('/')) {
    return path.startsWith(pattern);
  }
  return path === pattern;
}

/* ── Go source scanning ─────────────────────────────────────────── */

const problems: string[] = [];

/** Extract a top-level function body by brace matching from its `func` line. */
function extractFuncBody(source: string, signaturePrefix: string, where: string): string | null {
  const start = source.indexOf(signaturePrefix);
  if (start === -1) {
    problems.push(`${where}: could not find \`${signaturePrefix}\``);
    return null;
  }
  const open = source.indexOf('{', start);
  if (open === -1) {
    problems.push(`${where}: \`${signaturePrefix}\` has no body`);
    return null;
  }
  let depth = 0;
  for (let i = open; i < source.length; i++) {
    const ch = source[i];
    if (ch === '{') depth++;
    else if (ch === '}') {
      depth--;
      if (depth === 0) return source.slice(open + 1, i);
    }
  }
  problems.push(`${where}: unbalanced braces in \`${signaturePrefix}\``);
  return null;
}

/** Every `Path: "..."` literal in a chunk of Go source. */
function humaPaths(body: string): string[] {
  return [...body.matchAll(/\bPath:\s*"([^"]+)"/g)].map((m) => m[1] as string);
}

/** Every raw chi verb registration (`r.Post("/x", ...)`) in a chunk. */
function chiRoutes(body: string): string[] {
  const verbs = 'Get|Post|Put|Patch|Delete|Head|Options|Handle|Method|Mount';
  return [...body.matchAll(new RegExp(`\\b\\w+\\.(?:${verbs})\\(\\s*"([^"]+)"`, 'g'))].map(
    (m) => m[1] as string,
  );
}

/** Map an import alias in `router.go` to its package directory name. */
function importAliases(source: string): Map<string, string> {
  const aliases = new Map<string, string>();
  const importBlock = extractImportBlock(source);
  const line = /(?:([A-Za-z_]\w*)\s+)?"([^"]+)"/g;
  for (const m of importBlock.matchAll(line)) {
    const path = m[2] as string;
    const dir = path.split('/').pop();
    if (!dir) continue;
    aliases.set(m[1] ?? dir, dir);
  }
  return aliases;
}

function extractImportBlock(source: string): string {
  const start = source.indexOf('import (');
  if (start === -1) return '';
  const end = source.indexOf('\n)', start);
  return end === -1 ? '' : source.slice(start, end);
}

/**
 * Resolve a `pkg.RegisterPublic(...)` delegation to the paths it
 * registers. An unresolvable delegation is a failure, not a skip.
 */
function delegatedPaths(alias: string, aliases: Map<string, string>): string[] {
  const dir = aliases.get(alias);
  if (!dir) {
    problems.push(
      `router.go: public builder calls \`${alias}.RegisterPublic\` but no import maps to \`${alias}\``,
    );
    return [];
  }
  const candidate = resolve(handlersRoot, dir, 'register.go');
  let source: string;
  try {
    source = readFileSync(candidate, 'utf8');
  } catch {
    problems.push(
      `router.go: \`${alias}.RegisterPublic\` resolves to ${dir}, but ${candidate} is unreadable`,
    );
    return [];
  }
  const body = extractFuncBody(source, 'func RegisterPublic(', `${dir}/register.go`);
  if (body === null) return [];
  return humaPaths(body);
}

/**
 * Every route the public builder registers, from the source rather than
 * the spec.
 */
function scanPublicBuilder(): { humaPaths: string[]; rawRoutes: string[] } {
  const source = readFileSync(routerPath, 'utf8');
  const body = extractFuncBody(source, 'func buildPublicShareAPI(', 'router.go');
  if (body === null) return { humaPaths: [], rawRoutes: [] };

  const aliases = importAliases(source);
  const paths = humaPaths(body);

  for (const m of body.matchAll(/\b([A-Za-z_]\w*)\.RegisterPublic\(/g)) {
    paths.push(...delegatedPaths(m[1] as string, aliases));
  }

  // Any other `Register*` call in the builder registers routes this
  // script cannot see. Fail rather than under-report the public surface.
  for (const m of body.matchAll(/\b([A-Za-z_]\w*)\.(Register\w*)\(/g)) {
    const [, pkg, fn] = m as unknown as [string, string, string];
    if (pkg === 'huma' && fn === 'Register') continue;
    if (fn === 'RegisterPublic') continue;
    problems.push(
      `router.go: public builder calls \`${pkg}.${fn}\`, a registration form this check cannot resolve — teach the scanner or move the route out of the public group`,
    );
  }

  return { humaPaths: paths, rawRoutes: chiRoutes(body) };
}

/* ── Run ────────────────────────────────────────────────────────── */

const spec = JSON.parse(readFileSync(specPath, 'utf8')) as OpenApiSpec;
const allPaths = Object.keys(spec.paths ?? {}).sort();

const missingPublic = publicPatterns.filter(
  (pattern) => !allPaths.some((p) => matchesPattern(p, pattern)),
);

const leakedIntoPublicNamespace = allPaths.filter(
  (p) =>
    publicPrefixes.some((pref) => p.startsWith(pref)) &&
    !publicPatterns.some((pattern) => matchesPattern(p, pattern)),
);

const registered = scanPublicBuilder();

const unlistedPublic = registered.humaPaths.filter(
  (p) => !flowApiPublicPaths.some((pattern) => matchesPattern(p, pattern)),
);
const unlistedRaw = registered.rawRoutes.filter((p) => !publicRawRoutes.includes(p));

// The reverse of `missingPublic`, read from the source: an allowlist
// entry nothing registers is a stale exemption that would quietly bless
// the path if it came back. Scoped to the flow-api list, the only part
// this scan can speak for.
const registeredSet = new Set(registered.humaPaths);
const staleAllowlist =
  registered.humaPaths.length > 0 ? flowApiPublicPaths.filter((p) => !registeredSet.has(p)) : [];

let failed = false;

if (problems.length > 0) {
  failed = true;
  console.error('check-public-router: could not read the public surface with confidence:');
  for (const p of problems) console.error(`  - ${p}`);
}

if (missingPublic.length > 0) {
  failed = true;
  console.error('check-public-router: missing expected public path(s) from the spec:');
  for (const p of missingPublic) console.error(`  - ${p}`);
}

if (unlistedPublic.length > 0) {
  failed = true;
  console.error(
    'check-public-router: the public builder registers path(s) that are not allowlisted. These answer without a session — move the route to the authenticated builder, or add it here deliberately:',
  );
  for (const p of unlistedPublic) console.error(`  - ${p}`);
}

if (unlistedRaw.length > 0) {
  failed = true;
  console.error(
    'check-public-router: the public builder registers raw chi route(s) that are not allowlisted. These are invisible to the OpenAPI spec:',
  );
  for (const p of unlistedRaw) console.error(`  - ${p}`);
}

if (leakedIntoPublicNamespace.length > 0) {
  failed = true;
  console.error(
    'check-public-router: path(s) inside a public namespace are not on the allowlist; either move the route out of the public chi.Group or extend publicPatterns deliberately:',
  );
  for (const p of leakedIntoPublicNamespace) console.error(`  - ${p}`);
}

if (staleAllowlist.length > 0) {
  failed = true;
  console.error(
    'check-public-router: allowlisted path(s) that the public builder no longer registers. Remove them so the allowlist keeps meaning what it says:',
  );
  for (const p of staleAllowlist) console.error(`  - ${p}`);
}

if (failed) {
  process.exit(1);
}

console.info(
  `check-public-router: ${registered.humaPaths.length} public operation(s) and ${registered.rawRoutes.length} raw route(s) registered, all allowlisted; ${allPaths.length} spec paths checked`,
);
