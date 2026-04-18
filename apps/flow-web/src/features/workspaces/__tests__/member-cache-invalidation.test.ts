/**
 * Verify that workspace member mutations invalidate both the members
 * list AND the users list (actor pickers, assignee dropdowns, etc.).
 *
 * Bug fixed: useAddMember, useUpdateMemberRole, and useRemoveMember
 * only invalidated workspacesKeys.members() but not workspacesKeys.users(),
 * causing stale actor pickers after member changes.
 */

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('workspace member mutation cache invalidation', () => {
  const source = readFileSync(resolve(__dirname, '../api.ts'), 'utf-8');

  it('useAddMember invalidates users query', () => {
    // Find the useAddMember function and its onSuccess
    const addMemberStart = source.indexOf('function useAddMember');
    expect(addMemberStart).toBeGreaterThan(-1);

    // Extract the function body (up to the next export)
    const nextExport = source.indexOf('export', addMemberStart + 10);
    const fnBody = source.slice(addMemberStart, nextExport);

    expect(fnBody).toContain('workspacesKeys.members(');
    expect(fnBody).toContain('workspacesKeys.users(');
  });

  it('useUpdateMemberRole invalidates users query', () => {
    const updateRoleStart = source.indexOf('function useUpdateMemberRole');
    expect(updateRoleStart).toBeGreaterThan(-1);

    const nextExport = source.indexOf('export', updateRoleStart + 10);
    const fnBody = source.slice(updateRoleStart, nextExport);

    expect(fnBody).toContain('workspacesKeys.members(');
    expect(fnBody).toContain('workspacesKeys.users(');
  });

  it('useRemoveMember invalidates users query', () => {
    const removeStart = source.indexOf('function useRemoveMember');
    expect(removeStart).toBeGreaterThan(-1);

    // RemoveMember is the last function, so take until end of file
    const fnBody = source.slice(removeStart);

    expect(fnBody).toContain('workspacesKeys.members(');
    expect(fnBody).toContain('workspacesKeys.users(');
  });
});
