import { readFileSync } from 'node:fs';

interface PackageMetadata {
  version?: unknown;
}

export function getPackageVersion(): string {
  try {
    const raw = readFileSync(new URL('../package.json', import.meta.url), 'utf8');
    const pkg = JSON.parse(raw) as PackageMetadata;
    if (typeof pkg.version === 'string' && pkg.version.length > 0) {
      return pkg.version;
    }
  } catch {
    // Fall through to the development marker when package metadata is unavailable.
  }
  return 'dev';
}
