// Prints an nginx snippet that sets $spa_script_hash to the CSP hash source of
// the single inline script in a built index.html. Fails unless there is exactly
// one, because the CSP allows nothing else inline.
// Usage: bun csp-script-hash.mjs <path to index.html>
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

const file = process.argv[2];
const html = readFileSync(file, 'utf8');
const hashes = [];
for (const [, attributes, body] of html.matchAll(/<script([^>]*)>([\s\S]*?)<\/script>/g)) {
  if (/\ssrc\s*=/.test(attributes)) continue;
  hashes.push(`'sha256-${createHash('sha256').update(body).digest('base64')}'`);
}
if (hashes.length !== 1) {
  console.error(`${file}: expected exactly 1 inline script, found ${hashes.length}`);
  process.exit(1);
}
process.stdout.write(`set $spa_script_hash "${hashes[0]}";\n`);
