import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import { getPackageVersion } from '../src/version.js';

describe('getPackageVersion', () => {
  it('reads the CLI package version instead of returning a hardcoded literal', () => {
    const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')) as {
      version: string;
    };

    expect(getPackageVersion()).toBe(pkg.version);
  });
});
