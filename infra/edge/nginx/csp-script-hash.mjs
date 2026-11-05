// Prints an nginx snippet that sets the named variable to the CSP hash source
// of the single inline script in a built index.html. Fails unless there is exactly
// one, because the CSP allows nothing else inline.
// Usage: bun csp-script-hash.mjs <path to index.html> <variable name>
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

const [file, variable] = process.argv.slice(2);
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
process.stdout.write(`set $${variable} "${hashes[0]}";\n`);
