/**
 * Unit tests for the deprecated-flag alias resolver in
 * `src/util/flags.ts`.
 *
 * The CLI exposes a canonical `--workspace-id` (alias `-w`) and a
 * deprecated `--workspace` form (kept for backward compat during the
 * pre-1.0 window). `@libraz/node-cli`'s parser stores each long option
 * under its literal name, so both forms appear in `ctx.options` as
 * distinct keys. `resolveDeprecatedFlag` collapses them into one value
 * and emits a one-line stderr warning when the deprecated form was
 * used.
 */

import { Writable } from 'node:stream';
import { describe, expect, it } from 'vitest';

import { resolveDeprecatedFlag } from '../src/util/flags.js';

/**
 * Capture-mode `Writable` used to assert what the CLI prints. Strings
 * are concatenated into `output` regardless of the chunk encoding.
 */
class CaptureStream extends Writable {
  output = '';

  override _write(
    chunk: Buffer | string,
    _encoding: BufferEncoding,
    callback: (error?: Error | null) => void,
  ): void {
    this.output += typeof chunk === 'string' ? chunk : chunk.toString('utf-8');
    callback();
  }
}

describe('resolveDeprecatedFlag', () => {
  it('returns undefined when neither form is supplied', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      {},
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    expect(value).toBeUndefined();
    expect(stderr.output).toBe('');
  });

  it('returns the canonical value silently when --workspace-id is supplied', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { 'workspace-id': 'ws-canonical' },
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    expect(value).toBe('ws-canonical');
    expect(stderr.output).toBe('');
  });

  it('accepts the deprecated --workspace form and emits a deprecation warning', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { workspace: 'ws-legacy' },
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    expect(value).toBe('ws-legacy');
    // The warning mentions both flag forms so the user knows what to migrate to.
    expect(stderr.output).toMatch(/--workspace is deprecated/);
    expect(stderr.output).toMatch(/--workspace-id/);
  });

  it('warns on the deprecated form even when the canonical form also wins', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { 'workspace-id': 'ws-canonical', workspace: 'ws-legacy' },
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    // Canonical wins when both are supplied.
    expect(value).toBe('ws-canonical');
    // But we still tell the user to drop --workspace.
    expect(stderr.output).toMatch(/--workspace is deprecated/);
  });

  it('handles --project / --project-id symmetrically', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { project: 'proj-legacy' },
      'project-id',
      'project',
      '--project-id',
      '--project',
      stderr,
    );
    expect(value).toBe('proj-legacy');
    expect(stderr.output).toMatch(/--project is deprecated/);
    expect(stderr.output).toMatch(/--project-id/);
  });

  it('ignores empty-string deprecated values (treated as not supplied)', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { workspace: '' },
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    expect(value).toBeUndefined();
    expect(stderr.output).toBe('');
  });

  it('ignores non-string deprecated values', () => {
    const stderr = new CaptureStream();
    const value = resolveDeprecatedFlag(
      { workspace: 42 },
      'workspace-id',
      'workspace',
      '--workspace-id',
      '--workspace',
      stderr,
    );
    expect(value).toBeUndefined();
    expect(stderr.output).toBe('');
  });
});
