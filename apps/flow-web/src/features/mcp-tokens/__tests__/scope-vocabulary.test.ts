/**
 * The MCP scope vocabulary lives in Go and never reaches the OpenAPI
 * schema (the request field is a bare string array), so there is no
 * generated artifact tying the two together. This test is that tie: it
 * reads `mcp.SupportedScopes` out of the Go source and holds the list the
 * dialog offers to it.
 *
 * Without it, a scope added in Go is one no user can grant, and a scope
 * removed from Go is one the create dialog offers and the server refuses.
 */

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

import { DEFAULT_MCP_TOKEN_SCOPES, MCP_TOKEN_SCOPE_OPTIONS, MCP_TOKEN_SCOPES } from '../scopes';

// Vitest runs with apps/flow-web as the working directory (see the
// `test` script), matching the other source-reading guards in this suite.
const SCOPES_GO = join(process.cwd(), '../flow-api/internal/mcp/scopes.go');

/**
 * Pulls the scope string literals out of the const block in scopes.go.
 * The file declares them one per line as `Name = "value"`, so the values
 * are read by splitting on the quote character rather than matching a
 * pattern (regex is avoided in resolution paths project-wide).
 */
function goScopeLiterals(source: string): string[] {
  const out: string[] = [];
  for (const line of source.split('\n')) {
    if (!line.includes('Scope') || !line.includes('=')) continue;
    const parts = line.split('"');
    const value = parts[1];
    if (parts.length === 3 && value !== undefined && value.includes(':')) {
      out.push(value);
    }
  }
  return out;
}

describe('MCP token scope vocabulary', () => {
  const declared = goScopeLiterals(readFileSync(SCOPES_GO, 'utf8'));

  it('finds the scope constants in the Go source', () => {
    expect(declared.length).toBeGreaterThan(0);
  });

  it('offers exactly the scopes the server supports', () => {
    expect([...MCP_TOKEN_SCOPES].sort()).toEqual([...declared].sort());
  });

  it('has a checkbox for every supported scope', () => {
    expect(MCP_TOKEN_SCOPE_OPTIONS.map((o) => o.scope).sort()).toEqual([...declared].sort());
  });

  it('defaults to a non-empty selection, since an empty token can call nothing', () => {
    expect(DEFAULT_MCP_TOKEN_SCOPES.length).toBeGreaterThan(0);
    for (const scope of DEFAULT_MCP_TOKEN_SCOPES) {
      expect(declared).toContain(scope);
    }
  });
});
