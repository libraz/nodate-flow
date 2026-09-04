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
 * The check has three halves, and they fail for different reasons:
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
 *   3. No response schema reachable from a public route names a person.
 *      Halves 1 and 2 police which doors are open; this one polices what
 *      walks out of them. A share link is handed around, forwarded and
 *      indexed, and its holder has no identity, so there is nobody the
 *      response can be trusted to name: a display name, an address, an
 *      avatar or a person's public id all identify a human being to an
 *      audience that was never granted one. The route set comes from
 *      half 2's source scan, so a route added to the public group is
 *      body-checked the moment it exists rather than when someone
 *      remembers to list it.
 *
 * What half 3 counts as naming a person, and why that shape:
 *
 *   - A person role used as a property name, alone or carrying a person
 *     attribute: `assignee`, `attendees`, `creatorName`, `userEmail`,
 *     `ownerAvatarUrl`, `actorId`. The role vocabulary is what makes the
 *     rule narrow enough to need no exemptions — the attribute half
 *     (`name`, `id`, `email`, `avatar`, …) is exactly the set of words a
 *     resource also uses about itself, so it only counts when a role
 *     word puts a human on the other side of it.
 *   - `*By` audit attribution and its variants — `createdBy`,
 *     `revokedByName`, `updatedById`. These name whoever acted, which is
 *     a person even when the surrounding resource is not.
 *   - Standalone properties that cannot describe anything but a human:
 *     `email`, `firstName`, `avatarUrl`, `phoneNumber`, `username`.
 *
 *   Person public ids are in scope deliberately. An opaque id looks
 *   harmless and is not: correlated across two share links it re-links
 *   the same human, and the last leak of this shape travelled as an
 *   assignee id rather than as a name.
 *
 *   A resource's own `id`, `name`, `title` and `description` are not
 *   matched, and neither are `displayName` or `iconUrl` on their own —
 *   a lens, a calendar and a share page legitimately have all of those.
 *   A person embedded as an object is caught at the property that holds
 *   it (`assignee`, `user`), not at the fields inside it, so nothing is
 *   lost by leaving the bare words alone.
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
 * The self-verification cases run on every invocation, before the spec
 * and the router are read, and a failure among them stops the run. Every
 * half of this check can go quiet rather than fail: a pattern that no
 * longer matches how a route is registered yields an empty public set,
 * and an empty public set is allowlisted, body-checked and compliant by
 * definition. So each half asserts its own input is non-empty and
 * reports no verdict when it is not, and the cases include a positive
 * control that feeds half 3 a schema graph it must reject — proving the
 * scanner read something is not proving it can still detect something.
 *
 * Usage:
 *
 *   bun run scripts/check-public-router.ts
 *
 * Exit codes:
 *   0 — public surface matches the allowlist and names no person
 *   1 — read the input and found a violation, see stderr
 *   2 — could not reach a verdict: an input was unreadable or empty, so
 *       the run proves nothing and must not be read as a pass
 */

import assert from 'node:assert/strict';
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
  components?: { schemas?: Record<string, unknown> };
}

/** A JSON Schema node as it appears in the merged document. */
type SchemaNode = Record<string, unknown>;

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

/* ── Person-shaped property names ───────────────────────────────── */

/**
 * Words that put a human on the other side of a property. A role word is
 * what narrows the attribute vocabulary below from "every field a
 * resource has" to "a field about a person", so the two are only ever
 * matched together — or the role alone, which holds the person itself.
 */
const personRoles: ReadonlyArray<string> = [
  'actor',
  'admin',
  'assignee',
  'assigner',
  'attendee',
  'author',
  'collaborator',
  'contact',
  'creator',
  'follower',
  'guest',
  'host',
  'invitee',
  'inviter',
  'member',
  'organizer',
  'owner',
  'participant',
  'person',
  'recipient',
  'reporter',
  'requester',
  'reviewer',
  'sender',
  'subscriber',
  'user',
  'watcher',
];

/**
 * Attributes that identify the person a role word points at. Each is a
 * word a resource also uses about itself, which is why none of them
 * counts on its own.
 */
const personAttributes: ReadonlyArray<string> = [
  'avatar',
  'avatarurl',
  'displayname',
  'email',
  'emailaddress',
  'emails',
  'fullname',
  'handle',
  'icon',
  'iconurl',
  'id',
  'ids',
  'image',
  'imageurl',
  'initials',
  'mail',
  'name',
  'names',
  'nickname',
  'photo',
  'photourl',
  'picture',
  'pictureurl',
  'publicid',
  'publicids',
  'username',
];

/**
 * Verbs whose `*By` form attributes an action to whoever performed it.
 * `createdBy` names a person even on a resource that has no other person
 * on it, so these are matched independently of the role vocabulary.
 */
const attributionVerbs: ReadonlyArray<string> = [
  'accepted',
  'approved',
  'archived',
  'assigned',
  'canceled',
  'cancelled',
  'closed',
  'completed',
  'created',
  'deleted',
  'invited',
  'locked',
  'modified',
  'performed',
  'published',
  'rejected',
  'requested',
  'resolved',
  'revoked',
  'sent',
  'shared',
  'submitted',
  'triggered',
  'updated',
  'uploaded',
];

/**
 * Properties that cannot describe anything but a human whatever they sit
 * on. Deliberately excludes `displayName`, `icon*` and `name` — a
 * calendar, a lens and a share page each legitimately have those, and a
 * person carrying them is caught at the role-named property holding it.
 */
const standalonePersonKeys: ReadonlyArray<string> = [
  'avatar',
  'avatarurl',
  'email',
  'emailaddress',
  'emails',
  'familyname',
  'firstname',
  'fullname',
  'givenname',
  'gravatarurl',
  'lastname',
  'middlename',
  'nickname',
  'phone',
  'phonenumber',
  'photourl',
  'pictureurl',
  'profileimageurl',
  'realname',
  'surname',
  'username',
];

function buildPersonKeys(): ReadonlyMap<string, string> {
  const keys = new Map<string, string>();
  const add = (key: string, reason: string) => {
    if (!keys.has(key)) keys.set(key, reason);
  };

  for (const role of personRoles) {
    add(role, `\`${role}\` holds a person`);
    add(`${role}s`, `\`${role}s\` holds people`);
    for (const attr of personAttributes) {
      add(`${role}${attr}`, `\`${role}\` + \`${attr}\` identifies a person`);
      add(`${role}s${attr}`, `\`${role}s\` + \`${attr}\` identifies people`);
    }
  }

  for (const verb of attributionVerbs) {
    add(`${verb}by`, `\`${verb}By\` attributes an action to a person`);
    for (const attr of personAttributes) {
      add(`${verb}by${attr}`, `\`${verb}By\` + \`${attr}\` identifies the person who acted`);
    }
  }

  for (const key of standalonePersonKeys) {
    add(key, `\`${key}\` describes a person and nothing else`);
  }

  return keys;
}

const personKeys = buildPersonKeys();

/**
 * Compare property names without their separators, so `assignee_email`,
 * `assigneeEmail` and `AssigneeEmail` are one key.
 */
function normalizeKey(property: string): string {
  return property.replace(/[^A-Za-z0-9]/g, '').toLowerCase();
}

/** Why this property name identifies a human, or null if it does not. */
function personKeyReason(property: string): string | null {
  return personKeys.get(normalizeKey(property)) ?? null;
}

/* ── Public response bodies ─────────────────────────────────────── */

interface PersonLeak {
  path: string;
  property: string;
  trail: string;
  reason: string;
}

/**
 * Every response body schema a path serves, across all of its methods,
 * status codes and content types. Returned unresolved — `$ref`s are
 * followed during the walk, where the cycle guard lives.
 */
function responseSchemas(pathItem: Record<string, unknown>): SchemaNode[] {
  const out: SchemaNode[] = [];
  for (const operation of Object.values(pathItem)) {
    if (!operation || typeof operation !== 'object') continue;
    const responses = (operation as Record<string, unknown>).responses;
    if (!responses || typeof responses !== 'object') continue;
    for (const response of Object.values(responses as Record<string, unknown>)) {
      if (!response || typeof response !== 'object') continue;
      const content = (response as Record<string, unknown>).content;
      if (!content || typeof content !== 'object') continue;
      for (const media of Object.values(content as Record<string, unknown>)) {
        if (!media || typeof media !== 'object') continue;
        const schema = (media as Record<string, unknown>).schema;
        if (schema && typeof schema === 'object') out.push(schema as SchemaNode);
      }
    }
  }
  return out;
}

/**
 * Walk a schema to a fixpoint, following `$ref`s, array items, map
 * values and composition keywords, and report every property name that
 * identifies a person. `$ref` graphs in this document are recursive in
 * places, so a schema name is walked once per call and revisiting it is
 * a no-op rather than a hang.
 */
function findPersonKeys(spec: OpenApiSpec, root: SchemaNode, rootTrail: string): PersonLeak[] {
  const found: PersonLeak[] = [];
  const visited = new Set<string>();
  const components = (spec.components?.schemas ?? {}) as Record<string, SchemaNode>;

  const walk = (node: unknown, trail: string): void => {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      for (const item of node) walk(item, trail);
      return;
    }
    const schema = node as SchemaNode;

    const ref = schema.$ref;
    if (typeof ref === 'string') {
      const name = ref.split('/').pop() ?? '';
      if (visited.has(name)) return;
      visited.add(name);
      walk(components[name], `${trail} -> ${name}`);
      return;
    }

    const properties = schema.properties;
    if (properties && typeof properties === 'object') {
      for (const [property, child] of Object.entries(properties as Record<string, unknown>)) {
        const reason = personKeyReason(property);
        if (reason !== null) {
          found.push({ path: '', property, trail: `${trail}.${property}`, reason });
        }
        walk(child, `${trail}.${property}`);
      }
    }

    walk(schema.items, `${trail}[]`);
    walk(schema.prefixItems, `${trail}[]`);
    if (typeof schema.additionalProperties === 'object') {
      walk(schema.additionalProperties, `${trail}{}`);
    }
    for (const keyword of ['allOf', 'anyOf', 'oneOf', 'not'] as const) {
      walk(schema[keyword], trail);
    }
  };

  walk(root, rootTrail);
  return found;
}

/**
 * Scan the response bodies of a set of paths. Returns the leaks and the
 * number of schemas actually resolved, so the caller can tell "nothing
 * names a person" apart from "nothing was read".
 */
function scanPublicResponses(
  spec: OpenApiSpec,
  paths: ReadonlyArray<string>,
): { leaks: PersonLeak[]; schemaCount: number } {
  const leaks: PersonLeak[] = [];
  let schemaCount = 0;
  for (const path of paths) {
    const pathItem = spec.paths?.[path];
    if (!pathItem) continue;
    for (const schema of responseSchemas(pathItem)) {
      schemaCount++;
      for (const leak of findPersonKeys(spec, schema, path)) {
        leaks.push({ ...leak, path });
      }
    }
  }
  return { leaks, schemaCount };
}

/* ── Go source scanning ─────────────────────────────────────────── */

const problems: string[] = [];

/**
 * Extract a top-level function body by brace matching from its `func`
 * line. Failures are recorded in `sink` rather than returned, so a body
 * this script could not read is reported instead of read as empty; the
 * sink is a parameter so the self-verification cases can inspect it
 * without writing into the run's own problem list.
 */
function extractFuncBody(
  source: string,
  signaturePrefix: string,
  where: string,
  sink: string[] = problems,
): string | null {
  const start = source.indexOf(signaturePrefix);
  if (start === -1) {
    sink.push(`${where}: could not find \`${signaturePrefix}\``);
    return null;
  }
  const open = source.indexOf('{', start);
  if (open === -1) {
    sink.push(`${where}: \`${signaturePrefix}\` has no body`);
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
  sink.push(`${where}: unbalanced braces in \`${signaturePrefix}\``);
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

/**
 * `pkg.Register*` calls in a builder body that this script cannot resolve
 * to a set of paths. Returned as `pkg.Fn` pairs; `huma.Register` is the
 * inline form and `RegisterPublic` is the delegation form, both of which
 * are read elsewhere.
 */
function unresolvedRegistrations(body: string): string[] {
  const out: string[] = [];
  for (const m of body.matchAll(/\b([A-Za-z_]\w*)\.(Register\w*)\(/g)) {
    const pkg = m[1] as string;
    const fn = m[2] as string;
    if (pkg === 'huma' && fn === 'Register') continue;
    if (fn === 'RegisterPublic') continue;
    out.push(`${pkg}.${fn}`);
  }
  return out;
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
  for (const call of unresolvedRegistrations(body)) {
    problems.push(
      `router.go: public builder calls \`${call}\`, a registration form this check cannot resolve — teach the scanner or move the route out of the public group`,
    );
  }

  return { humaPaths: paths, rawRoutes: chiRoutes(body) };
}

/* ── Self-verification ──────────────────────────────────────────────
 *
 * Runs before the spec and the router are read, every time.
 */

function selfCheck(): string[] {
  const BUILDER = [
    'func buildPublicShareAPI(r chi.Router, deps Deps) {',
    '\thuma.Register(sub, huma.Operation{',
    '\t\tOperationID: "getPublicCalendar",',
    '\t\tPath:        "/share/cal/{token}",',
    '\t}, handleShare)',
    '\tif deps.Webhooks {',
    '\t\tr.Post("/webhooks/github", handleGithub)',
    '\t}',
    '\tlogger.Info("mounted /webhooks/github")',
    '\tlenses.RegisterPublic(sub, deps)',
    '}',
    '',
    'func buildAuthedAPI(r chi.Router, deps Deps) {',
    '\thuma.Register(sub, huma.Operation{Path: "/tasks"}, handleTasks)',
    '}',
    '',
  ].join('\n');

  /**
   * A spec-shaped graph the person-key half must judge correctly: one
   * body whose person sits behind a `$ref`, inside an array, on a schema
   * that refers back to itself, and one carrying nothing but the fields
   * a resource has about itself. The second is as load-bearing as the
   * first — a rule that fires on `name` or `iconUrl` would need
   * exemptions to pass the real spec, and an exemption list is how a
   * guard stops meaning anything.
   */
  const CONTROL_SPEC: OpenApiSpec = {
    paths: {
      '/control/leaky': {
        get: {
          responses: {
            '200': {
              content: {
                'application/json': { schema: { $ref: '#/components/schemas/controlPage' } },
              },
            },
          },
        },
      },
      '/control/clean': {
        get: {
          responses: {
            '200': {
              content: {
                'application/json': { schema: { $ref: '#/components/schemas/controlClean' } },
              },
            },
          },
        },
      },
    },
    components: {
      schemas: {
        controlPage: {
          type: 'object',
          properties: {
            rows: { type: 'array', items: { $ref: '#/components/schemas/controlRow' } },
          },
        },
        controlRow: {
          type: 'object',
          properties: {
            id: { type: 'string' },
            title: { type: 'string' },
            assigneeName: { type: 'string' },
            children: { type: 'array', items: { $ref: '#/components/schemas/controlRow' } },
          },
        },
        controlClean: {
          type: 'object',
          properties: {
            id: { type: 'string' },
            name: { type: 'string' },
            title: { type: 'string' },
            description: { type: 'string' },
            iconUrl: { type: 'string' },
            coverUrl: { type: 'string' },
            workspaceName: { type: 'string' },
            calendarName: { type: 'string' },
            userAction: { type: 'string' },
          },
        },
      },
    },
  };

  const cases: ReadonlyArray<[string, () => void]> = [
    [
      'reads a huma path and a raw chi route out of a builder body',
      () => {
        const sink: string[] = [];
        const body = extractFuncBody(BUILDER, 'func buildPublicShareAPI(', 'sample.go', sink);
        assert.deepEqual(sink, []);
        assert.deepEqual(humaPaths(body ?? ''), ['/share/cal/{token}']);
        assert.deepEqual(chiRoutes(body ?? ''), ['/webhooks/github']);
      },
    ],
    [
      'stops at the builder own closing brace',
      () => {
        const sink: string[] = [];
        const body = extractFuncBody(BUILDER, 'func buildPublicShareAPI(', 'sample.go', sink);
        assert.equal(humaPaths(body ?? '').includes('/tasks'), false);
      },
    ],
    [
      'records a body it could not read instead of returning an empty one',
      () => {
        const sink: string[] = [];
        assert.equal(extractFuncBody(BUILDER, 'func buildGoneAPI(', 'sample.go', sink), null);
        assert.equal(sink.length, 1);
        assert.match(sink[0] ?? '', /could not find/);
      },
    ],
    [
      'reports a registration form it cannot resolve and passes the two it can',
      () => {
        assert.deepEqual(unresolvedRegistrations(BUILDER), []);
        assert.deepEqual(unresolvedRegistrations('\tadmin.RegisterAll(sub, deps)\n'), [
          'admin.RegisterAll',
        ]);
      },
    ],
    [
      'detects a person behind a $ref, inside an array, in a self-referential graph',
      () => {
        const { leaks, schemaCount } = scanPublicResponses(CONTROL_SPEC, ['/control/leaky']);
        assert.equal(schemaCount, 1);
        assert.deepEqual(
          leaks.map((l) => l.property),
          ['assigneeName'],
        );
        assert.equal(
          leaks[0]?.trail,
          '/control/leaky -> controlPage.rows[] -> controlRow.assigneeName',
        );
      },
    ],
    [
      'leaves a body that carries only the resource own fields alone',
      () => {
        const { leaks, schemaCount } = scanPublicResponses(CONTROL_SPEC, ['/control/clean']);
        assert.equal(schemaCount, 1);
        assert.deepEqual(leaks, []);
      },
    ],
  ];

  const failures: string[] = [];
  for (const [name, run] of cases) {
    try {
      run();
    } catch (err) {
      failures.push(`  ${name}\n    ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  return failures;
}

const selfFailures = selfCheck();
if (selfFailures.length > 0) {
  console.error(
    `check-public-router: ${selfFailures.length} self-verification case(s) failed, so the public surface was not read:\n`,
  );
  for (const f of selfFailures) console.error(f);
  console.error(
    '\nThe scanner itself is wrong. Fix it before trusting anything it says about the router.',
  );
  process.exit(1);
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

// Half 3 body-checks the routes half 2 read out of the builder, plus the
// invite routes another service contributes to the same unauthenticated
// surface. The flow-api side is deliberately not read from the allowlist:
// a route added to the public group is checked because it is registered,
// not because someone remembered to list it.
const bodyCheckedPaths = [...new Set([...registered.humaPaths, ...externalPublicPaths])];
const { leaks: personLeaks, schemaCount: publicSchemaCount } = scanPublicResponses(
  spec,
  bodyCheckedPaths,
);

// An input that came back empty means a half ran over nothing, and
// nothing is compliant by definition. Each of these makes the run prove
// less than it claims, so they end it without a verdict.
const verdictBlockers: string[] = [...problems];

if (allPaths.length === 0) {
  verdictBlockers.push(`the merged spec at ${specPath} declares no paths`);
}
if (registered.humaPaths.length === 0) {
  verdictBlockers.push(
    'the public builder scan found no huma operations — the registration patterns no longer match the router source, and an empty public set passes every check that reads it',
  );
}
if (publicSchemaCount === 0) {
  verdictBlockers.push(
    `no response schema resolved for the ${bodyCheckedPaths.length} public path(s), so no response body was actually inspected for person-shaped fields`,
  );
}

let failed = false;

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

if (personLeaks.length > 0) {
  failed = true;
  console.error(
    'check-public-router: public response schema(s) name a person. The holder of a share link has no identity and was never granted one, so drop the field from the DTO rather than filling it conditionally:',
  );
  for (const leak of personLeaks) {
    console.error(`  - ${leak.trail} — ${leak.reason}`);
  }
}

if (verdictBlockers.length > 0) {
  console.error(
    'check-public-router: could not reach a verdict on the public surface, so this run proves nothing:',
  );
  for (const b of verdictBlockers) console.error(`  - ${b}`);
  process.exit(2);
}

if (failed) {
  process.exit(1);
}

console.info(
  `check-public-router: ${registered.humaPaths.length} public operation(s) and ${registered.rawRoutes.length} raw route(s) registered, all allowlisted; ${publicSchemaCount} response schema(s) across ${bodyCheckedPaths.length} public path(s) name no person; ${allPaths.length} spec paths checked`,
);
